package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/vctt94/poker-bisonrelay/poker"
	"github.com/vctt94/poker-bisonrelay/rpc/grpc/pokerrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements both PokerService and LobbyService
type Server struct {
	pokerrpc.UnimplementedPokerServiceServer
	pokerrpc.UnimplementedLobbyServiceServer
	db     Database
	tables map[string]*poker.Table
	mu     sync.RWMutex
}

// NewServer creates a new poker server
func NewServer(db Database) *Server {
	return &Server{
		db:     db,
		tables: make(map[string]*poker.Table),
	}
}

// LobbyService methods

func (s *Server) CreateTable(ctx context.Context, req *pokerrpc.CreateTableRequest) (*pokerrpc.CreateTableResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if player has enough balance
	balance, err := s.db.GetPlayerBalance(req.PlayerId)
	if err != nil {
		return nil, err
	}

	if balance < req.BuyIn {
		return nil, status.Error(codes.FailedPrecondition, "insufficient balance for buy-in")
	}

	// Create table config
	cfg := poker.TableConfig{
		ID:         fmt.Sprintf("table-%d", time.Now().UnixNano()),
		CreatorID:  req.PlayerId,
		BuyIn:      req.BuyIn,
		MinPlayers: int(req.MinPlayers),
		MaxPlayers: int(req.MaxPlayers),
		SmallBlind: req.SmallBlind,
		BigBlind:   req.BigBlind,
		MinBalance: req.MinBalance,
		TimeBank:   5 * time.Second,
	}

	// Create new table
	table := poker.NewTable(cfg)

	// Add creator as first player
	err = table.AddPlayer(req.PlayerId, balance)
	if err != nil {
		return nil, err
	}

	s.tables[cfg.ID] = table

	return &pokerrpc.CreateTableResponse{TableId: cfg.ID}, nil
}

func (s *Server) JoinTable(ctx context.Context, req *pokerrpc.JoinTableRequest) (*pokerrpc.JoinTableResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	table, ok := s.tables[req.TableId]
	if !ok {
		return &pokerrpc.JoinTableResponse{Success: false, Message: "Table not found"}, nil
	}

	// Check if player has enough balance
	balance, err := s.db.GetPlayerBalance(req.PlayerId)
	if err != nil {
		return nil, err
	}

	if balance < table.GetConfig().MinBalance {
		return &pokerrpc.JoinTableResponse{Success: false, Message: "Insufficient balance"}, nil
	}

	// Add player to table
	err = table.AddPlayer(req.PlayerId, balance)
	if err != nil {
		return &pokerrpc.JoinTableResponse{Success: false, Message: err.Error()}, nil
	}

	// Update player's balance in the database
	err = s.db.UpdatePlayerBalance(req.PlayerId, -table.GetConfig().BuyIn, "table buy-in", "joined table")
	if err != nil {
		// If balance update fails, remove player from table
		table.RemovePlayer(req.PlayerId)
		return nil, err
	}

	return &pokerrpc.JoinTableResponse{
		Success:    true,
		Message:    "Successfully joined table",
		NewBalance: balance - table.GetConfig().BuyIn,
	}, nil
}

func (s *Server) LeaveTable(ctx context.Context, req *pokerrpc.LeaveTableRequest) (*pokerrpc.LeaveTableResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	table, ok := s.tables[req.TableId]
	if !ok {
		return &pokerrpc.LeaveTableResponse{Success: false, Message: "Table not found"}, nil
	}

	// Get player's current balance
	player := table.GetPlayer(req.PlayerId)
	if player == nil {
		return &pokerrpc.LeaveTableResponse{Success: false, Message: "Player not at table"}, nil
	}

	// Remove player from table
	err := table.RemovePlayer(req.PlayerId)
	if err != nil {
		return &pokerrpc.LeaveTableResponse{Success: false, Message: err.Error()}, nil
	}

	// Refund buy-in if game hasn't started
	refundAmount := int64(0)
	if !table.IsGameStarted() {
		refundAmount = table.GetConfig().BuyIn
		// Update player's balance in the database
		err = s.db.UpdatePlayerBalance(req.PlayerId, refundAmount, "table refund", "left table")
		if err != nil {
			return nil, err
		}
	}

	return &pokerrpc.LeaveTableResponse{
		Success:      true,
		Message:      "Successfully left table",
		RefundAmount: refundAmount,
	}, nil
}

func (s *Server) GetTables(ctx context.Context, req *pokerrpc.GetTablesRequest) (*pokerrpc.GetTablesResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tables := make([]*pokerrpc.Table, 0, len(s.tables))
	for _, table := range s.tables {
		// Convert poker.Table to pokerrpc.Table
		config := table.GetConfig()
		protoTable := &pokerrpc.Table{
			Id:              config.ID,
			HostId:          config.CreatorID,
			SmallBlind:      config.SmallBlind,
			BigBlind:        config.BigBlind,
			MaxPlayers:      int32(table.GetMaxPlayers()),
			MinPlayers:      int32(table.GetMinPlayers()),
			CurrentPlayers:  int32(len(table.GetPlayers())),
			MinBalance:      config.MinBalance,
			BuyIn:           config.BuyIn,
			GameStarted:     table.IsGameStarted(),
			AllPlayersReady: table.AreAllPlayersReady(),
		}
		tables = append(tables, protoTable)
	}

	return &pokerrpc.GetTablesResponse{Tables: tables}, nil
}

func (s *Server) GetBalance(ctx context.Context, req *pokerrpc.GetBalanceRequest) (*pokerrpc.GetBalanceResponse, error) {
	balance, err := s.db.GetPlayerBalance(req.PlayerId)
	if err != nil {
		if err.Error() == "player not found" {
			return nil, status.Error(codes.NotFound, "player not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pokerrpc.GetBalanceResponse{Balance: balance}, nil
}

func (s *Server) UpdateBalance(ctx context.Context, req *pokerrpc.UpdateBalanceRequest) (*pokerrpc.UpdateBalanceResponse, error) {
	err := s.db.UpdatePlayerBalance(req.PlayerId, req.Amount, req.Description, "balance update")
	if err != nil {
		return nil, err
	}

	balance, err := s.db.GetPlayerBalance(req.PlayerId)
	if err != nil {
		return nil, err
	}

	return &pokerrpc.UpdateBalanceResponse{
		NewBalance: balance,
		Message:    "Balance updated successfully",
	}, nil
}

func (s *Server) ProcessTip(ctx context.Context, req *pokerrpc.ProcessTipRequest) (*pokerrpc.ProcessTipResponse, error) {
	err := s.db.UpdatePlayerBalance(req.FromPlayerId, -req.Amount, req.Message, "tip sent")
	if err != nil {
		return nil, err
	}
	err = s.db.UpdatePlayerBalance(req.ToPlayerId, req.Amount, req.Message, "tip received")
	if err != nil {
		return nil, err
	}

	balance, err := s.db.GetPlayerBalance(req.ToPlayerId)
	if err != nil {
		return nil, err
	}

	return &pokerrpc.ProcessTipResponse{
		Success:    true,
		Message:    "Tip processed successfully",
		NewBalance: balance,
	}, nil
}

func (s *Server) SetPlayerReady(ctx context.Context, req *pokerrpc.SetPlayerReadyRequest) (*pokerrpc.SetPlayerReadyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	table, ok := s.tables[req.TableId]
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	player := table.GetPlayer(req.PlayerId)
	if player == nil {
		return nil, status.Error(codes.NotFound, "player not found at table")
	}

	player.IsReady = true
	allReady := table.CheckAllPlayersReady()

	// If all players are ready and the game hasn't started yet, start the game
	if allReady && !table.IsGameStarted() {
		_ = table.StartGame() // Ignore error for now (handled internally)
	}

	return &pokerrpc.SetPlayerReadyResponse{
		Success:         true,
		Message:         "Player is ready",
		AllPlayersReady: allReady,
	}, nil
}

func (s *Server) SetPlayerUnready(ctx context.Context, req *pokerrpc.SetPlayerUnreadyRequest) (*pokerrpc.SetPlayerUnreadyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	table, ok := s.tables[req.TableId]
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	player := table.GetPlayer(req.PlayerId)
	if player == nil {
		return nil, status.Error(codes.NotFound, "player not found at table")
	}

	player.IsReady = false

	return &pokerrpc.SetPlayerUnreadyResponse{
		Success: true,
		Message: "Player is unready",
	}, nil
}

// PokerService methods

func (s *Server) StartGameStream(req *pokerrpc.StartGameStreamRequest, stream pokerrpc.PokerService_StartGameStreamServer) error {
	s.mu.RLock()
	table, ok := s.tables[req.TableId]
	s.mu.RUnlock()

	if !ok {
		return status.Error(codes.NotFound, "table not found")
	}

	// Subscribe to game updates
	updates := table.Subscribe(stream.Context())

	for update := range updates {
		if err := stream.Send(update); err != nil {
			return err
		}
	}

	return nil
}

func (s *Server) MakeBet(ctx context.Context, req *pokerrpc.MakeBetRequest) (*pokerrpc.MakeBetResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	table, ok := s.tables[req.TableId]
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	err := table.MakeBet(req.PlayerId, req.Amount)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	balance, err := s.db.GetPlayerBalance(req.PlayerId)
	if err != nil {
		return nil, err
	}

	return &pokerrpc.MakeBetResponse{
		Success:    true,
		Message:    "Bet placed successfully",
		NewBalance: balance,
	}, nil
}

func (s *Server) Fold(ctx context.Context, req *pokerrpc.FoldRequest) (*pokerrpc.FoldResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	table, ok := s.tables[req.TableId]
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	player := table.GetPlayer(req.PlayerId)
	if player == nil {
		return nil, status.Error(codes.NotFound, "player not found at table")
	}

	// Mark player as folded
	player.HasFolded = true
	player.LastAction = time.Now()

	// Check if the folding action completes the current betting round and thus advances the phase.
	table.MaybeAdvancePhase()

	return &pokerrpc.FoldResponse{
		Success: true,
		Message: "Player folded",
	}, nil
}

func (s *Server) Check(ctx context.Context, req *pokerrpc.CheckRequest) (*pokerrpc.CheckResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	table, ok := s.tables[req.TableId]
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	err := table.MakeBet(req.PlayerId, 0)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return &pokerrpc.CheckResponse{
		Success: true,
		Message: "Player checked",
	}, nil
}

func (s *Server) GetGameState(ctx context.Context, req *pokerrpc.GetGameStateRequest) (*pokerrpc.GetGameStateResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	table, ok := s.tables[req.TableId]
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	// Process any player timeouts before building the state.
	table.HandleTimeouts()

	players := make([]*pokerrpc.Player, 0, len(table.GetPlayers()))
	for _, p := range table.GetPlayers() {
		players = append(players, &pokerrpc.Player{
			Id:      p.ID,
			Balance: p.Balance,
			IsReady: p.IsReady,
			Folded:  p.HasFolded,
		})
	}

	// Build community cards slice
	communityCards := make([]*pokerrpc.Card, 0)
	if tGame := table.GetGame(); tGame != nil {
		for _, c := range tGame.GetCommunityCards() {
			communityCards = append(communityCards, &pokerrpc.Card{
				Suit:  c.GetSuit(),
				Value: c.GetValue(),
			})
		}
	}

	return &pokerrpc.GetGameStateResponse{
		GameState: &pokerrpc.GameUpdate{
			TableId:         table.GetConfig().ID,
			Phase:           table.GetGamePhase(),
			Players:         players,
			CommunityCards:  communityCards,
			CurrentBet:      table.GetCurrentBet(),
			Pot:             table.GetPot(),
			GameStarted:     table.IsGameStarted(),
			PlayersRequired: int32(table.GetMinPlayers()),
			PlayersJoined:   int32(len(table.GetPlayers())),
		},
	}, nil
}

func (s *Server) EvaluateHand(ctx context.Context, req *pokerrpc.EvaluateHandRequest) (*pokerrpc.EvaluateHandResponse, error) {
	// TODO: Implement hand evaluation
	return &pokerrpc.EvaluateHandResponse{
		Rank:        pokerrpc.HandRank_HIGH_CARD,
		Description: "High Card",
		BestHand:    req.Cards,
	}, nil
}

func (s *Server) GetWinners(ctx context.Context, req *pokerrpc.GetWinnersRequest) (*pokerrpc.GetWinnersResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	table, ok := s.tables[req.TableId]
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	// If game hasn't started or is nil, return empty winners.
	if !table.IsGameStarted() || table.GetGame() == nil {
		return &pokerrpc.GetWinnersResponse{
			Winners: []*pokerrpc.Winner{},
			Pot:     0,
		}, nil
	}

	// Determine last active player (non-folded)
	var winnerID string
	for _, p := range table.GetPlayers() {
		if !p.HasFolded {
			winnerID = p.ID
			break
		}
	}

	pot := table.GetPot()
	// Subtract any uncalled bet (currentBet) still in the middle if applicable.
	currentBet := table.GetCurrentBet()
	if currentBet > 0 && pot >= currentBet {
		pot -= currentBet
	}

	winners := []*pokerrpc.Winner{}
	if winnerID != "" {
		winners = append(winners, &pokerrpc.Winner{
			PlayerId: winnerID,
			Winnings: pot,
		})
	}

	return &pokerrpc.GetWinnersResponse{
		Winners: winners,
		Pot:     pot,
	}, nil
}

// StartNotificationStream handles notification streaming
func (s *Server) StartNotificationStream(req *pokerrpc.StartNotificationStreamRequest, stream pokerrpc.LobbyService_StartNotificationStreamServer) error {
	// Implementation for notification streaming
	// This would typically involve subscribing to a notification channel
	// and sending notifications as they occur
	playerID := req.PlayerId
	if playerID == "" {
		return status.Error(codes.InvalidArgument, "player ID is required")
	}

	// Send an initial notification to ensure the stream is established
	initialNotification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_UNKNOWN,
		Message:  "Connected to notification stream",
		PlayerId: playerID,
	}
	if err := stream.Send(initialNotification); err != nil {
		return err
	}

	// Keep the stream open with periodic heartbeat notifications
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	ctx := stream.Context()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// Send periodic updates for all tables this player is in
			s.mu.RLock()
			playerHasTables := false

			for _, table := range s.tables {
				// Only send updates for tables that this player is in
				if table.GetPlayer(playerID) != nil {
					playerHasTables = true
					players := make([]*pokerrpc.Player, 0, len(table.GetPlayers()))
					for _, p := range table.GetPlayers() {
						players = append(players, &pokerrpc.Player{
							Id:      p.ID,
							Balance: p.Balance,
							IsReady: p.IsReady,
							Folded:  p.HasFolded,
						})
					}

					// Determine notification type based on game phase
					var notificationType pokerrpc.NotificationType
					switch table.GetGamePhase() {
					case pokerrpc.GamePhase_WAITING:
						notificationType = pokerrpc.NotificationType_PLAYER_READY
					case pokerrpc.GamePhase_PRE_FLOP:
						notificationType = pokerrpc.NotificationType_GAME_STARTED
					case pokerrpc.GamePhase_FLOP, pokerrpc.GamePhase_TURN, pokerrpc.GamePhase_RIVER:
						notificationType = pokerrpc.NotificationType_NEW_ROUND
					case pokerrpc.GamePhase_SHOWDOWN:
						notificationType = pokerrpc.NotificationType_SHOWDOWN_RESULT
					default:
						notificationType = pokerrpc.NotificationType_UNKNOWN
					}

					notification := &pokerrpc.Notification{
						Type:     notificationType,
						Message:  fmt.Sprintf("Game update for table %s: Phase=%s, Players=%d", table.GetConfig().ID, table.GetGamePhase(), len(players)),
						TableId:  table.GetConfig().ID,
						PlayerId: playerID,
					}

					// Send to client
					if err := stream.Send(notification); err != nil {
						s.mu.RUnlock()
						return err
					}
				}
			}

			// If player isn't at any tables, send a heartbeat notification
			if !playerHasTables {
				heartbeatNotification := &pokerrpc.Notification{
					Type:     pokerrpc.NotificationType_UNKNOWN,
					Message:  "Heartbeat",
					PlayerId: playerID,
				}
				if err := stream.Send(heartbeatNotification); err != nil {
					s.mu.RUnlock()
					return err
				}
			}
			s.mu.RUnlock()
		}
	}
}

package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/vctt94/poker-bisonrelay/pkg/poker"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
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
		HostID:     req.PlayerId,
		BuyIn:      req.BuyIn,
		MinPlayers: int(req.MinPlayers),
		MaxPlayers: int(req.MaxPlayers),
		SmallBlind: req.SmallBlind,
		BigBlind:   req.BigBlind,
		MinBalance: req.MinBalance,
		TimeBank:   30 * time.Second,
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

	// Check if the leaving player is the host of the table
	config := table.GetConfig()
	isHost := req.PlayerId == config.HostID

	// Remove player from table
	err := table.RemovePlayer(req.PlayerId)
	if err != nil {
		return &pokerrpc.LeaveTableResponse{Success: false, Message: err.Error()}, nil
	}

	// Refund buy-in if game hasn't started
	refundAmount := int64(0)
	if !table.IsGameStarted() {
		refundAmount = config.BuyIn
		// Update player's balance in the database
		err = s.db.UpdatePlayerBalance(req.PlayerId, refundAmount, "table refund", "left table")
		if err != nil {
			return nil, err
		}
	}

	// If the host leaves, close the table completely
	if isHost {
		// Refund remaining players who haven't started playing
		if !table.IsGameStarted() {
			remainingPlayers := table.GetPlayers()
			for _, p := range remainingPlayers {
				// Refund their buy-in
				err = s.db.UpdatePlayerBalance(p.ID, table.GetConfig().BuyIn, "table closed by host", "host left table")
				if err != nil {
					// Log error but continue with table closure
					// In a production system, you might want better error handling here
				}
			}
		}

		// Remove the table from the server
		delete(s.tables, req.TableId)

		return &pokerrpc.LeaveTableResponse{
			Success: true,
			Message: "Host left - table closed",
		}, nil
	}

	return &pokerrpc.LeaveTableResponse{
		Success: true,
		Message: "Successfully left table",
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
			HostId:          config.HostID,
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

func (s *Server) GetPlayerCurrentTable(ctx context.Context, req *pokerrpc.GetPlayerCurrentTableRequest) (*pokerrpc.GetPlayerCurrentTableResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Search through all tables to find if the player is in any table
	for _, table := range s.tables {
		if table.GetPlayer(req.PlayerId) != nil {
			return &pokerrpc.GetPlayerCurrentTableResponse{
				TableId: table.GetConfig().ID,
			}, nil
		}
	}

	// Player is not in any table, return empty table ID
	return &pokerrpc.GetPlayerCurrentTableResponse{
		TableId: "",
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

	// Subscribe to game updates with the requesting player ID
	updates := table.Subscribe(stream.Context(), req.PlayerId)

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

	// Check if game is running
	if !table.IsGameStarted() {
		return nil, status.Error(codes.FailedPrecondition, "game not started")
	}

	// Verify it's the player's turn
	currentPlayerID := table.GetCurrentPlayerID()
	if currentPlayerID != req.PlayerId {
		return nil, status.Error(codes.FailedPrecondition, "not your turn")
	}

	// Handle the fold action
	err := table.HandleFold(req.PlayerId)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to process fold: "+err.Error())
	}

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

	// Check if game is running
	if !table.IsGameStarted() {
		return nil, status.Error(codes.FailedPrecondition, "game not started")
	}

	// Verify it's the player's turn
	currentPlayerID := table.GetCurrentPlayerID()
	if currentPlayerID != req.PlayerId {
		return nil, status.Error(codes.FailedPrecondition, "not your turn")
	}

	// Check action is essentially a bet of 0 - call MakeBet with current bet amount
	// This ensures the check is properly integrated with the betting logic
	currentBet := table.GetCurrentBet()
	err := table.MakeBet(req.PlayerId, currentBet)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to process check: "+err.Error())
	}

	return &pokerrpc.CheckResponse{
		Success: true,
		Message: "Player checked",
	}, nil
}

// buildPlayerForUpdate creates a Player proto message with appropriate card visibility
func (s *Server) buildPlayerForUpdate(p *poker.Player, requestingPlayerID string, game *poker.Game) *pokerrpc.Player {
	player := &pokerrpc.Player{
		Id:         p.ID,
		Balance:    p.Balance,
		IsReady:    p.IsReady,
		Folded:     p.HasFolded,
		CurrentBet: p.HasBet,
	}

	// Only include hand cards if this is the requesting player's own data
	// or if the game is in showdown phase
	if (p.ID == requestingPlayerID) || (game != nil && game.GetPhase() == pokerrpc.GamePhase_SHOWDOWN) {
		player.Hand = make([]*pokerrpc.Card, len(p.Hand))
		for i, card := range p.Hand {
			player.Hand[i] = &pokerrpc.Card{
				Suit:  card.GetSuit(),
				Value: card.GetValue(),
			}
		}
	}

	return player
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

	// Extract requesting player ID from context metadata
	requestingPlayerID := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if playerIDs := md.Get("player-id"); len(playerIDs) > 0 {
			requestingPlayerID = playerIDs[0]
		}
	}

	game := table.GetGame()
	players := make([]*pokerrpc.Player, 0, len(table.GetPlayers()))
	for _, p := range table.GetPlayers() {
		// Create player update with complete information
		player := &pokerrpc.Player{
			Id:         p.ID,
			Balance:    p.Balance,
			IsReady:    p.IsReady,
			Folded:     p.HasFolded,
			CurrentBet: p.HasBet,
		}

		// Find the corresponding game player to get the hand cards
		var gamePlayer *poker.Player
		if game != nil {
			for _, gp := range game.GetPlayers() {
				if gp.ID == p.ID {
					gamePlayer = gp
					break
				}
			}
		}

		// Only include hand cards if this is the requesting player's own data
		// or if the game is in showdown phase
		if (p.ID == requestingPlayerID) || (game != nil && game.GetPhase() == pokerrpc.GamePhase_SHOWDOWN) {
			if gamePlayer != nil {
				player.Hand = make([]*pokerrpc.Card, len(gamePlayer.Hand))
				for i, card := range gamePlayer.Hand {
					player.Hand[i] = &pokerrpc.Card{
						Suit:  card.GetSuit(),
						Value: card.GetValue(),
					}
				}
			}
		}

		players = append(players, player)
	}

	// Build community cards slice
	communityCards := make([]*pokerrpc.Card, 0)
	if game != nil {
		for _, c := range game.GetCommunityCards() {
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
			CurrentPlayer:   table.GetCurrentPlayerID(),
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

	// Keep the stream open and wait for context cancellation
	// Notifications will be sent through specific events rather than polling
	ctx := stream.Context()
	<-ctx.Done()
	return nil
}

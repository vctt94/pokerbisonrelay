package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/decred/slog"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/poker-bisonrelay/pkg/poker"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// NotificationStream represents a client's notification stream
type NotificationStream struct {
	playerID string
	stream   pokerrpc.LobbyService_StartNotificationStreamServer
	done     chan struct{}
}

// Server implements both PokerService and LobbyService
type Server struct {
	pokerrpc.UnimplementedPokerServiceServer
	pokerrpc.UnimplementedLobbyServiceServer
	log        slog.Logger
	logBackend *logging.LogBackend
	db         Database
	tables     map[string]*poker.Table
	mu         sync.RWMutex

	// Notification streaming
	notificationStreams map[string]*NotificationStream
	notificationMu      sync.RWMutex

	// Game streaming
	gameStreams   map[string]map[string]pokerrpc.PokerService_StartGameStreamServer // tableID -> playerID -> stream
	gameStreamsMu sync.RWMutex
}

// NewServer creates a new poker server
func NewServer(db Database, logBackend *logging.LogBackend) *Server {
	return &Server{
		log:                 logBackend.Logger("SERVER"),
		logBackend:          logBackend,
		db:                  db,
		tables:              make(map[string]*poker.Table),
		notificationStreams: make(map[string]*NotificationStream),
		gameStreams:         make(map[string]map[string]pokerrpc.PokerService_StartGameStreamServer),
	}
}

// LobbyService methods

func (s *Server) CreateTable(ctx context.Context, req *pokerrpc.CreateTableRequest) (*pokerrpc.CreateTableResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if player has enough DCR balance for the buy-in (buy-in is in DCR atoms)
	dcrBalance, err := s.db.GetPlayerBalance(req.PlayerId)
	if err != nil {
		return nil, err
	}

	// req.BuyIn is the DCR amount required to join the table (in atoms)
	// This is what gets deducted from the player's DCR account balance
	if dcrBalance < req.BuyIn {
		return nil, status.Error(codes.FailedPrecondition, "insufficient DCR balance for buy-in")
	}

	// Calculate starting chips (use request value or default to buy-in)
	startingChips := req.StartingChips
	if startingChips == 0 {
		startingChips = 1000 // Default fallback
	}

	// Calculate timebank duration (use request value or default to 30 seconds)
	timeBankDuration := time.Duration(req.TimeBankSeconds) * time.Second
	if req.TimeBankSeconds == 0 {
		timeBankDuration = 30 * time.Second // Default 30 seconds
	}

	// Create table config
	cfg := poker.TableConfig{
		ID:            fmt.Sprintf("table-%d", time.Now().UnixNano()),
		Log:           s.logBackend.Logger("TABLE"),
		HostID:        req.PlayerId,
		BuyIn:         req.BuyIn, // DCR amount (in atoms) to join table
		MinPlayers:    int(req.MinPlayers),
		MaxPlayers:    int(req.MaxPlayers),
		SmallBlind:    req.SmallBlind, // Poker chips
		BigBlind:      req.BigBlind,   // Poker chips
		MinBalance:    req.MinBalance, // Minimum DCR balance required
		StartingChips: startingChips,  // Poker chips given to each player
		TimeBank:      timeBankDuration,
	}

	// Create new table
	table := poker.NewTable(cfg)

	// Set up event manager with notification sender
	table.SetNotificationSender(s)

	// Add creator as first player with starting chips (not DCR balance)
	err = table.AddPlayer(req.PlayerId, startingChips)
	if err != nil {
		return nil, err
	}

	// Deduct buy-in from creator's DCR account balance
	err = s.db.UpdatePlayerBalance(req.PlayerId, -req.BuyIn, "table buy-in", "created table")
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

	// Check if player has enough DCR balance for the buy-in
	dcrBalance, err := s.db.GetPlayerBalance(req.PlayerId)
	if err != nil {
		return nil, err
	}

	config := table.GetConfig()

	// Check if player has enough DCR for the buy-in
	if dcrBalance < config.BuyIn {
		return &pokerrpc.JoinTableResponse{
			Success: false,
			Message: "Insufficient DCR balance for buy-in",
		}, nil
	}

	// Add player to table with starting chips (not DCR balance)
	err = table.AddPlayer(req.PlayerId, config.StartingChips)
	if err != nil {
		return &pokerrpc.JoinTableResponse{Success: false, Message: err.Error()}, nil
	}

	// Deduct buy-in from player's DCR account balance
	err = s.db.UpdatePlayerBalance(req.PlayerId, -config.BuyIn, "table buy-in", "joined table")
	if err != nil {
		// If balance update fails, remove player from table
		table.RemovePlayer(req.PlayerId)
		return nil, err
	}

	return &pokerrpc.JoinTableResponse{
		Success:    true,
		Message:    "Successfully joined table",
		NewBalance: dcrBalance - config.BuyIn, // Return new DCR balance
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

	// Trigger player ready event so other players see the update immediately
	table.TriggerPlayerReadyEvent(req.PlayerId, true)

	allReady := table.CheckAllPlayersReady()

	// If all players are ready and the game hasn't started yet, start the game
	if allReady && !table.IsGameStarted() {
		_ = table.StartGame() // This will send GAME_STARTED notification immediately
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

	// Trigger player unready event so other players see the update immediately
	table.TriggerPlayerReadyEvent(req.PlayerId, false)

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
	// Register the stream
	s.gameStreamsMu.Lock()
	if s.gameStreams[req.TableId] == nil {
		s.gameStreams[req.TableId] = make(map[string]pokerrpc.PokerService_StartGameStreamServer)
	}
	s.gameStreams[req.TableId][req.PlayerId] = stream
	s.gameStreamsMu.Unlock()

	// Remove stream when done
	defer func() {
		s.gameStreamsMu.Lock()
		if tableStreams, exists := s.gameStreams[req.TableId]; exists {
			delete(tableStreams, req.PlayerId)
			if len(tableStreams) == 0 {
				delete(s.gameStreams, req.TableId)
			}
		}
		s.gameStreamsMu.Unlock()
	}()

	// Send initial game state
	gameState, err := s.buildGameState(req.TableId, req.PlayerId)
	if err != nil {
		return err
	}

	if err := stream.Send(gameState); err != nil {
		return err
	}

	// Keep stream open and wait for context cancellation
	// Game state updates will be sent via the notification system when events occur
	ctx := stream.Context()
	<-ctx.Done()
	return nil
}

// buildGameState creates a GameUpdate for the requesting player
func (s *Server) buildGameState(tableID, requestingPlayerID string) (*pokerrpc.GameUpdate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	table, ok := s.tables[tableID]
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	// Process any player timeouts before building the state
	table.HandleTimeouts()

	game := table.GetGame()

	// Use table players as the single source of truth
	tablePlayers := table.GetPlayers()
	players := make([]*pokerrpc.Player, 0, len(tablePlayers))
	for _, p := range tablePlayers {
		// Create player update with complete information
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

	var currentPlayerID string
	if table.IsGameStarted() && game != nil {
		currentPlayerID = table.GetCurrentPlayerID()
	}

	gameUpdate := &pokerrpc.GameUpdate{
		TableId:         tableID,
		Phase:           table.GetGamePhase(),
		Players:         players,
		CommunityCards:  communityCards,
		Pot:             table.GetPot(),
		CurrentBet:      table.GetCurrentBet(),
		CurrentPlayer:   currentPlayerID,
		GameStarted:     table.IsGameStarted(),
		PlayersRequired: int32(table.GetMinPlayers()),
		PlayersJoined:   int32(len(table.GetPlayers())),
	}

	return gameUpdate, nil
}

func (s *Server) MakeBet(ctx context.Context, req *pokerrpc.MakeBetRequest) (*pokerrpc.MakeBetResponse, error) {
	s.mu.Lock()

	table, ok := s.tables[req.TableId]
	if !ok {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, "table not found")
	}

	// Check if game is running
	if !table.IsGameStarted() {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "game not started")
	}

	// Verify it's the player's turn
	currentPlayerID := table.GetCurrentPlayerID()
	if currentPlayerID != req.PlayerId {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "not your turn")
	}

	err := table.MakeBet(req.PlayerId, req.Amount)
	if err != nil {
		s.mu.Unlock()
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	s.mu.Unlock()

	// Broadcast bet notification to all players at the table (outside of server lock)
	s.broadcastNotificationToTable(req.TableId, &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_BET_MADE,
		PlayerId: req.PlayerId,
		TableId:  req.TableId,
		Amount:   req.Amount,
		Message:  fmt.Sprintf("Player %s bet %d chips", req.PlayerId, req.Amount),
	})

	// Broadcast updated game state to all players
	s.BroadcastGameStateUpdate(req.TableId)

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

	table, ok := s.tables[req.TableId]
	if !ok {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, "table not found")
	}

	// Check if game is running
	if !table.IsGameStarted() {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "game not started")
	}

	// Verify it's the player's turn
	currentPlayerID := table.GetCurrentPlayerID()
	if currentPlayerID != req.PlayerId {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "not your turn")
	}

	// Handle the fold action
	err := table.HandleFold(req.PlayerId)
	if err != nil {
		s.mu.Unlock()
		return nil, status.Error(codes.Internal, "failed to process fold: "+err.Error())
	}

	s.mu.Unlock()

	// Broadcast fold notification to all players at the table (outside of server lock)
	s.broadcastNotificationToTable(req.TableId, &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_PLAYER_FOLDED,
		PlayerId: req.PlayerId,
		TableId:  req.TableId,
		Message:  fmt.Sprintf("Player %s folded", req.PlayerId),
	})

	// Broadcast updated game state to all players
	s.BroadcastGameStateUpdate(req.TableId)

	return &pokerrpc.FoldResponse{
		Success: true,
		Message: "Folded successfully",
	}, nil
}

// Call implements the Call RPC method
func (s *Server) Call(ctx context.Context, req *pokerrpc.CallRequest) (*pokerrpc.CallResponse, error) {
	s.mu.Lock()

	table, ok := s.tables[req.TableId]
	if !ok {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, "table not found")
	}

	// Check if game is running
	if !table.IsGameStarted() {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "game not started")
	}

	// Verify it's the player's turn
	currentPlayerID := table.GetCurrentPlayerID()
	if currentPlayerID != req.PlayerId {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "not your turn")
	}

	// Handle the call action
	err := table.HandleCall(req.PlayerId)
	if err != nil {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	// Get the current bet amount for the notification
	currentBet := table.GetCurrentBet()

	s.mu.Unlock()

	// Broadcast call notification to all players at the table (outside of server lock)
	s.broadcastNotificationToTable(req.TableId, &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_CALL_MADE,
		PlayerId: req.PlayerId,
		TableId:  req.TableId,
		Amount:   currentBet,
		Message:  fmt.Sprintf("Player %s called %d chips", req.PlayerId, currentBet),
	})

	// Broadcast updated game state to all players
	s.BroadcastGameStateUpdate(req.TableId)

	return &pokerrpc.CallResponse{
		Success: true,
		Message: "Call successful",
	}, nil
}

// Check implements the Check RPC method
func (s *Server) Check(ctx context.Context, req *pokerrpc.CheckRequest) (*pokerrpc.CheckResponse, error) {
	s.mu.Lock()

	table, ok := s.tables[req.TableId]
	if !ok {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, "table not found")
	}

	// Check if game is running
	if !table.IsGameStarted() {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "game not started")
	}

	// Verify it's the player's turn
	currentPlayerID := table.GetCurrentPlayerID()
	if currentPlayerID != req.PlayerId {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "not your turn")
	}

	// Handle the check action
	err := table.HandleCheck(req.PlayerId)
	if err != nil {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	s.mu.Unlock()

	// Broadcast check notification to all players at the table (outside of server lock)
	s.broadcastNotificationToTable(req.TableId, &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_CHECK_MADE,
		PlayerId: req.PlayerId,
		TableId:  req.TableId,
		Amount:   0,
		Message:  fmt.Sprintf("Player %s checked", req.PlayerId),
	})

	// Broadcast updated game state to all players
	s.BroadcastGameStateUpdate(req.TableId)

	return &pokerrpc.CheckResponse{
		Success: true,
		Message: "Check successful",
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

	// Use table players as the single source of truth
	tablePlayers := table.GetPlayers()
	players := make([]*pokerrpc.Player, 0, len(tablePlayers))
	for _, p := range tablePlayers {
		// Create player update with complete information
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
			TableId:         req.TableId,
			Phase:           table.GetGamePhase(),
			Players:         players,
			CommunityCards:  communityCards,
			Pot:             table.GetPot(),
			CurrentBet:      table.GetCurrentBet(),
			CurrentPlayer:   table.GetCurrentPlayerID(),
			GameStarted:     table.IsGameStarted(),
			PlayersRequired: int32(table.GetMinPlayers()),
			PlayersJoined:   int32(len(tablePlayers)),
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

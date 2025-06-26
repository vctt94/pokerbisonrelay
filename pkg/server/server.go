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

	// Get creator's DCR balance
	creatorBalance, err := s.db.GetPlayerBalance(req.PlayerId)
	if err != nil {
		return nil, err
	}

	// Check if creator has enough DCR for the buy-in
	if creatorBalance < req.BuyIn {
		return nil, fmt.Errorf("insufficient DCR balance for buy-in: need %d, have %d", req.BuyIn, creatorBalance)
	}

	// Create table configuration
	timeBank := time.Duration(req.TimeBankSeconds) * time.Second
	if timeBank == 0 {
		timeBank = 30 * time.Second // Default to 30 seconds if not specified
	}

	// Handle StartingChips default logic
	startingChips := req.StartingChips
	if startingChips == 0 {
		startingChips = 1000 // Default to 1000 poker chips when not specified
	}

	cfg := poker.TableConfig{
		ID:             fmt.Sprintf("table_%d", time.Now().UnixNano()),
		Log:            s.log,
		HostID:         req.PlayerId,
		BuyIn:          req.BuyIn,
		MinPlayers:     int(req.MinPlayers),
		MaxPlayers:     int(req.MaxPlayers),
		SmallBlind:     req.SmallBlind,
		BigBlind:       req.BigBlind,
		MinBalance:     req.MinBalance,
		StartingChips:  startingChips, // Chips amount for the game with default logic
		TimeBank:       timeBank,
		AutoStartDelay: 5 * time.Second,
	}

	table := poker.NewTable(cfg)

	// Set up event manager with notification sender
	table.SetNotificationSender(s)

	// Add creator as first user
	_, err = table.AddNewUser(req.PlayerId, req.PlayerId, creatorBalance, 0)
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

	// Find next available seat
	users := table.GetUsers()
	occupiedSeats := make(map[int]bool)
	for _, user := range users {
		occupiedSeats[user.TableSeat] = true
	}

	nextSeat := 0
	for i := 0; i < config.MaxPlayers; i++ {
		if !occupiedSeats[i] {
			nextSeat = i
			break
		}
	}

	// Add user to table
	_, err = table.AddNewUser(req.PlayerId, req.PlayerId, dcrBalance, nextSeat)
	if err != nil {
		return &pokerrpc.JoinTableResponse{Success: false, Message: err.Error()}, nil
	}

	// Deduct buy-in from player's DCR account balance
	err = s.db.UpdatePlayerBalance(req.PlayerId, -config.BuyIn, "table buy-in", "joined table")
	if err != nil {
		// If balance update fails, remove player from table
		table.RemoveUser(req.PlayerId)
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

	// Get user's current balance
	user := table.GetUser(req.PlayerId)
	if user == nil {
		return &pokerrpc.LeaveTableResponse{Success: false, Message: "Player not at table"}, nil
	}

	// Check if the leaving player is the host of the table
	config := table.GetConfig()
	isHost := req.PlayerId == config.HostID

	// Remove user from table
	err := table.RemoveUser(req.PlayerId)
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
			remainingUsers := table.GetUsers()
			for _, u := range remainingUsers {
				// Refund their buy-in
				err = s.db.UpdatePlayerBalance(u.ID, table.GetConfig().BuyIn, "table closed by host", "host left table")
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
			CurrentPlayers:  int32(len(table.GetUsers())),
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

	user := table.GetUser(req.PlayerId)
	if user == nil {
		return nil, status.Error(codes.NotFound, "player not found at table")
	}

	user.IsReady = true

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

	user := table.GetUser(req.PlayerId)
	if user == nil {
		return nil, status.Error(codes.NotFound, "player not found at table")
	}

	user.IsReady = false

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
		if table.GetUser(req.PlayerId) != nil {
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

func (s *Server) ShowCards(ctx context.Context, req *pokerrpc.ShowCardsRequest) (*pokerrpc.ShowCardsResponse, error) {
	s.mu.RLock()
	table, ok := s.tables[req.TableId]
	s.mu.RUnlock()

	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	// Verify player is at the table
	user := table.GetUser(req.PlayerId)
	if user == nil {
		return nil, status.Error(codes.FailedPrecondition, "player not at table")
	}

	// Broadcast card visibility notification to all players at the table
	s.broadcastNotificationToTable(req.TableId, &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_CARDS_SHOWN,
		PlayerId: req.PlayerId,
		TableId:  req.TableId,
		Message:  fmt.Sprintf("%s is showing their cards", req.PlayerId),
	})

	return &pokerrpc.ShowCardsResponse{
		Success: true,
		Message: "Cards shown to other players",
	}, nil
}

func (s *Server) HideCards(ctx context.Context, req *pokerrpc.HideCardsRequest) (*pokerrpc.HideCardsResponse, error) {
	s.mu.RLock()
	table, ok := s.tables[req.TableId]
	s.mu.RUnlock()

	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	// Verify player is at the table
	user := table.GetUser(req.PlayerId)
	if user == nil {
		return nil, status.Error(codes.FailedPrecondition, "player not at table")
	}

	// Broadcast card visibility notification to all players at the table
	s.broadcastNotificationToTable(req.TableId, &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_CARDS_HIDDEN,
		PlayerId: req.PlayerId,
		TableId:  req.TableId,
		Message:  fmt.Sprintf("%s is hiding their cards", req.PlayerId),
	})

	return &pokerrpc.HideCardsResponse{
		Success: true,
		Message: "Cards hidden from other players",
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

	// Debug logging for game state transitions
	if game == nil {
		s.log.Debugf("buildGameState: game is nil for table %s, player %s - transition state", tableID, requestingPlayerID)
	} else {
		s.log.Debugf("buildGameState: game exists for table %s, player %s, phase: %v", tableID, requestingPlayerID, game.GetPhase())
	}

	return s.buildGameStateForPlayer(table, game, requestingPlayerID), nil
}

// buildGameStateForPlayer creates a GameUpdate with all the necessary data for a specific player
func (s *Server) buildGameStateForPlayer(table *poker.Table, game *poker.Game, requestingPlayerID string) *pokerrpc.GameUpdate {
	// Build players list from users and game players
	var players []*pokerrpc.Player
	if game != nil {
		players = s.buildPlayers(game.GetPlayers(), game, requestingPlayerID)
	} else {
		// If no game, build from table users
		users := table.GetUsers()
		players = make([]*pokerrpc.Player, 0, len(users))
		for _, user := range users {
			players = append(players, &pokerrpc.Player{
				Id:         user.ID,
				Balance:    user.AccountBalance,
				IsReady:    user.IsReady,
				Folded:     user.HasFolded,
				CurrentBet: user.HasBet,
				Hand:       make([]*pokerrpc.Card, 0), // Empty hand when no game
			})
		}
	}

	// Build community cards slice
	communityCards := make([]*pokerrpc.Card, 0)
	var pot int64 = 0
	if game != nil {
		pot = game.GetPot()
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

	return &pokerrpc.GameUpdate{
		TableId:         table.GetConfig().ID,
		Phase:           table.GetGamePhase(),
		Players:         players,
		CommunityCards:  communityCards,
		Pot:             pot,
		CurrentBet:      table.GetCurrentBet(),
		CurrentPlayer:   currentPlayerID,
		GameStarted:     table.IsGameStarted(),
		PlayersRequired: int32(table.GetMinPlayers()),
		PlayersJoined:   int32(len(table.GetUsers())),
	}
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

// buildPlayers creates a slice of Player proto messages with appropriate card visibility
func (s *Server) buildPlayers(tablePlayers []*poker.Player, game *poker.Game, requestingPlayerID string) []*pokerrpc.Player {
	players := make([]*pokerrpc.Player, 0, len(tablePlayers))
	for _, p := range tablePlayers {
		player := s.buildPlayerForUpdate(p, requestingPlayerID, game)
		players = append(players, player)
	}
	return players
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

	// Early return if game doesn't exist or player has no cards
	if game == nil {
		return player
	}

	// Show cards if it's the requesting player's own data (except during NEW_HAND_DEALING to avoid race conditions)
	// OR during showdown for all players
	if p.ID == requestingPlayerID {
		// Show own cards during all active game phases (not just showdown)
		if game.GetPhase() != pokerrpc.GamePhase_NEW_HAND_DEALING && len(p.Hand) > 0 {
			player.Hand = make([]*pokerrpc.Card, len(p.Hand))
			for i, card := range p.Hand {
				player.Hand[i] = &pokerrpc.Card{
					Suit:  card.GetSuit(),
					Value: card.GetValue(),
				}
			}
			s.log.Debugf("DEBUG: Showing %d cards for player %s (own cards, phase=%v)", len(p.Hand), p.ID, game.GetPhase())
		} else {
			// Debug: Log why cards are not being shown
			s.log.Debugf("DEBUG: Not showing cards for player %s: phase=%v, handSize=%d", p.ID, game.GetPhase(), len(p.Hand))
		}
	} else if game.GetPhase() == pokerrpc.GamePhase_SHOWDOWN && len(p.Hand) > 0 {
		// Show other players' cards only during showdown
		player.Hand = make([]*pokerrpc.Card, len(p.Hand))
		for i, card := range p.Hand {
			player.Hand[i] = &pokerrpc.Card{
				Suit:  card.GetSuit(),
				Value: card.GetValue(),
			}
		}
	}

	// Include hand description during showdown
	if game != nil && game.GetPhase() == pokerrpc.GamePhase_SHOWDOWN && p.HandDescription != "" {
		player.HandDescription = p.HandDescription
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

	return &pokerrpc.GetGameStateResponse{
		GameState: s.buildGameStateForPlayer(table, game, requestingPlayerID),
	}, nil
}

func (s *Server) EvaluateHand(ctx context.Context, req *pokerrpc.EvaluateHandRequest) (*pokerrpc.EvaluateHandResponse, error) {
	// Convert gRPC cards to internal Card format
	cards := make([]poker.Card, len(req.Cards))
	for i, grpcCard := range req.Cards {
		card, err := convertGRPCCardToInternal(grpcCard)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid card at index %d: %v", i, err))
		}
		cards[i] = card
	}

	// We need at least 5 cards to evaluate a hand
	if len(cards) < 5 {
		return nil, status.Error(codes.InvalidArgument, "need at least 5 cards to evaluate a hand")
	}

	// For hand evaluation, we'll treat the first 2 cards as hole cards
	// and the rest as community cards (this is a simplification)
	var holeCards, communityCards []poker.Card
	if len(cards) == 5 {
		// If exactly 5 cards, evaluate them all as community cards with empty hole cards
		holeCards = []poker.Card{}
		communityCards = cards
	} else if len(cards) >= 7 {
		// Standard Texas Hold'em: 2 hole + 5 community
		holeCards = cards[:2]
		communityCards = cards[2:7]
	} else {
		// 6 cards: 2 hole + 4 community (incomplete hand)
		holeCards = cards[:2]
		communityCards = cards[2:]
	}

	// Evaluate the hand
	handValue := poker.EvaluateHand(holeCards, communityCards)

	// Convert best hand back to gRPC format
	bestHandGRPC := make([]*pokerrpc.Card, len(handValue.BestHand))
	for i, card := range handValue.BestHand {
		bestHandGRPC[i] = &pokerrpc.Card{
			Suit:  card.GetSuit(),
			Value: card.GetValue(),
		}
	}

	return &pokerrpc.EvaluateHandResponse{
		Rank:        handValue.HandRank,
		Description: handValue.HandDescription,
		BestHand:    bestHandGRPC,
	}, nil
}

// convertGRPCCardToInternal converts a gRPC Card to internal Card format
func convertGRPCCardToInternal(grpcCard *pokerrpc.Card) (poker.Card, error) {
	if grpcCard == nil {
		return poker.Card{}, fmt.Errorf("card is nil")
	}

	// Convert suit string to internal Suit type
	var suit poker.Suit
	switch grpcCard.Suit {
	case "♠", "s", "S", "spades", "Spades":
		suit = poker.Spades
	case "♥", "h", "H", "hearts", "Hearts":
		suit = poker.Hearts
	case "♦", "d", "D", "diamonds", "Diamonds":
		suit = poker.Diamonds
	case "♣", "c", "C", "clubs", "Clubs":
		suit = poker.Clubs
	default:
		return poker.Card{}, fmt.Errorf("invalid suit: %s", grpcCard.Suit)
	}

	// Convert value string to internal Value type
	var value poker.Value
	switch grpcCard.Value {
	case "A", "a", "ace", "Ace":
		value = poker.Ace
	case "K", "k", "king", "King":
		value = poker.King
	case "Q", "q", "queen", "Queen":
		value = poker.Queen
	case "J", "j", "jack", "Jack":
		value = poker.Jack
	case "10", "T", "t", "ten", "Ten":
		value = poker.Ten
	case "9", "nine", "Nine":
		value = poker.Nine
	case "8", "eight", "Eight":
		value = poker.Eight
	case "7", "seven", "Seven":
		value = poker.Seven
	case "6", "six", "Six":
		value = poker.Six
	case "5", "five", "Five":
		value = poker.Five
	case "4", "four", "Four":
		value = poker.Four
	case "3", "three", "Three":
		value = poker.Three
	case "2", "two", "Two":
		value = poker.Two
	default:
		return poker.Card{}, fmt.Errorf("invalid value: %s", grpcCard.Value)
	}

	// Create the card using a helper function since fields are unexported
	return poker.NewCardFromSuitValue(suit, value), nil
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

	game := table.GetGame()
	pot := game.GetPot()

	// If the game is in showdown phase and we have tracked winners, use them
	gameWinners := game.GetWinners()
	if game.GetPhase() == pokerrpc.GamePhase_SHOWDOWN && len(gameWinners) > 0 {
		winners := []*pokerrpc.Winner{}
		for _, winnerID := range gameWinners {
			winners = append(winners, &pokerrpc.Winner{
				PlayerId: winnerID,
				Winnings: pot / int64(len(gameWinners)),
			})
		}
		return &pokerrpc.GetWinnersResponse{
			Winners: winners,
			Pot:     pot,
		}, nil
	}

	// If the game is not in showdown phase, determine winners based on current active players
	if game.GetPhase() != pokerrpc.GamePhase_SHOWDOWN {
		// Determine last active player (non-folded) from game players
		var winnerID string
		for _, p := range game.GetPlayers() {
			if !p.HasFolded {
				winnerID = p.ID
				break
			}
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

	// Fallback: no tracked winners in showdown phase - shouldn't happen but return empty
	return &pokerrpc.GetWinnersResponse{
		Winners: []*pokerrpc.Winner{},
		Pot:     pot,
	}, nil
}

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/decred/slog"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/poker-bisonrelay/pkg/poker"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/poker-bisonrelay/pkg/server/internal/db"
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
	server := &Server{
		log:                 logBackend.Logger("SERVER"),
		logBackend:          logBackend,
		db:                  db,
		tables:              make(map[string]*poker.Table),
		notificationStreams: make(map[string]*NotificationStream),
		gameStreams:         make(map[string]map[string]pokerrpc.PokerService_StartGameStreamServer),
	}

	// Load persisted tables on startup
	err := server.loadAllTables()
	if err != nil {
		server.log.Errorf("Failed to load persisted tables: %v", err)
	}

	return server
}

// saveTableStateAsync saves table state asynchronously to avoid blocking game operations
func (s *Server) saveTableStateAsync(tableID string, reason string) {
	go func() {
		err := s.saveTableState(tableID)
		if err != nil {
			s.log.Errorf("Failed to save table state for %s (%s): %v", tableID, reason, err)
		} else {
			s.log.Debugf("Saved table state for %s (trigger: %s)", tableID, reason)
		}
	}()
}

// SaveTableStateAsync implements the StateSaver interface for tables
func (s *Server) SaveTableStateAsync(tableID string, reason string) {
	s.saveTableStateAsync(tableID, reason)
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

	// Set up event manager with notification sender and state saver
	table.SetNotificationSender(s)
	table.SetStateSaver(s)

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

	config := table.GetConfig()

	// Check if player is already at the table (existing user or disconnected placeholder)
	existingUser := table.GetUser(req.PlayerId)
	if existingUser != nil {
		// Check if player was disconnected
		isDisconnected, err := s.db.IsPlayerDisconnected(req.TableId, req.PlayerId)
		if err == nil && isDisconnected {
			// Player is reconnecting - mark them as connected
			err = s.markPlayerConnected(req.TableId, req.PlayerId)
			if err != nil {
				s.log.Errorf("Failed to mark player as connected: %v", err)
			}

			// Save updated table state (async)
			s.saveTableStateAsync(req.TableId, "player reconnected")

			return &pokerrpc.JoinTableResponse{
				Success:    true,
				Message:    fmt.Sprintf("Reconnected to table. You have %d DCR balance.", existingUser.AccountBalance),
				NewBalance: 0, // DCR balance unchanged for reconnection
			}, nil
		} else if err != nil {
			// Player state not found in database, but they exist in table - treat as disconnected player reconnecting
			s.log.Warnf("Player %s exists in table %s but state not found in database, treating as reconnection", req.PlayerId, req.TableId)

			// Save player state to database to ensure consistency
			err = s.saveUserAsPlayerState(req.TableId, existingUser)
			if err != nil {
				s.log.Errorf("Failed to save player state during reconnection: %v", err)
			}

			// Mark them as connected
			err = s.markPlayerConnected(req.TableId, req.PlayerId)
			if err != nil {
				s.log.Errorf("Failed to mark player as connected: %v", err)
			}

			// Save updated table state (async)
			s.saveTableStateAsync(req.TableId, "player reconnected")

			return &pokerrpc.JoinTableResponse{
				Success:    true,
				Message:    fmt.Sprintf("Reconnected to table. You have %d DCR balance.", existingUser.AccountBalance),
				NewBalance: 0, // DCR balance unchanged for reconnection
			}, nil
		} else {
			// isDisconnected is false, but player might have lost connection without proper disconnection
			// For now, allow reconnection anyway since they're trying to join
			s.log.Warnf("Player %s exists in table %s and appears connected, but allowing rejoin (possible connection loss)", req.PlayerId, req.TableId)

			// Ensure player state exists in database
			err = s.saveUserAsPlayerState(req.TableId, existingUser)
			if err != nil {
				s.log.Errorf("Failed to save player state during rejoin: %v", err)
			}

			// Save updated table state (async)
			s.saveTableStateAsync(req.TableId, "player rejoined")

			return &pokerrpc.JoinTableResponse{
				Success:    true,
				Message:    fmt.Sprintf("Rejoined table. You have %d DCR balance.", existingUser.AccountBalance),
				NewBalance: 0, // DCR balance unchanged for rejoin
			}, nil
		}
	}

	// New player joining - check DCR balance
	dcrBalance, err := s.db.GetPlayerBalance(req.PlayerId)
	if err != nil {
		return nil, err
	}

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
	newUser, err := table.AddNewUser(req.PlayerId, req.PlayerId, dcrBalance, nextSeat)
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

	// Update the user's account balance to reflect the deduction
	newUser.AccountBalance = dcrBalance - config.BuyIn

	// Save player state to database (convert User to Player for database storage)
	if newUser != nil {
		err = s.saveUserAsPlayerState(req.TableId, newUser)
		if err != nil {
			s.log.Errorf("Failed to save new player state: %v", err)
		}
	}

	// Save updated table state (async)
	s.saveTableStateAsync(req.TableId, "player joined")

	return &pokerrpc.JoinTableResponse{
		Success:    true,
		Message:    "Successfully joined table",
		NewBalance: newUser.AccountBalance, // Return new DCR balance
	}, nil
}

func (s *Server) LeaveTable(ctx context.Context, req *pokerrpc.LeaveTableRequest) (*pokerrpc.LeaveTableResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	table, ok := s.tables[req.TableId]
	if !ok {
		return &pokerrpc.LeaveTableResponse{Success: false, Message: "Table not found"}, nil
	}

	// Get user's current state
	user := table.GetUser(req.PlayerId)
	if user == nil {
		return &pokerrpc.LeaveTableResponse{Success: false, Message: "Player not at table"}, nil
	}

	config := table.GetConfig()
	isHost := req.PlayerId == config.HostID

	// Check if player has chips in an active game
	var playerChips int64 = 0
	if table.IsGameStarted() && table.GetGame() != nil {
		// Find player in game to get their chip balance
		game := table.GetGame()
		for _, player := range game.GetPlayers() {
			if player.ID == req.PlayerId {
				playerChips = player.Balance
				break
			}
		}
	}

	// If game is in progress and player has chips, create placeholder instead of removing
	if table.IsGameStarted() && playerChips > 0 {
		// Mark player as disconnected but keep them in the game
		err := s.markPlayerDisconnected(req.TableId, req.PlayerId)
		if err != nil {
			s.log.Errorf("Failed to mark player as disconnected: %v", err)
		}

		// Save current game state (async)
		s.saveTableStateAsync(req.TableId, "player disconnected")

		return &pokerrpc.LeaveTableResponse{
			Success: true,
			Message: fmt.Sprintf("You have been disconnected but your seat is reserved. You have %d chips remaining.", playerChips),
		}, nil
	}

	// For players with no chips or when game hasn't started, remove completely
	err := table.RemoveUser(req.PlayerId)
	if err != nil {
		return &pokerrpc.LeaveTableResponse{Success: false, Message: err.Error()}, nil
	}

	// Delete player state from database
	err = s.db.DeletePlayerState(req.TableId, req.PlayerId)
	if err != nil {
		s.log.Errorf("Failed to delete player state from database: %v", err)
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

	// If the host leaves, transfer host to another player if available
	if isHost {
		remainingUsers := table.GetUsers()

		// If there are other users, transfer host to the first available user
		if len(remainingUsers) > 0 {
			// Find the first user that is not the leaving host
			var newHostID string
			for _, u := range remainingUsers {
				if u.ID != req.PlayerId {
					newHostID = u.ID
					break
				}
			}

			if newHostID != "" {
				// Transfer host ownership by updating the config
				err = s.transferTableHost(req.TableId, newHostID)
				if err != nil {
					return &pokerrpc.LeaveTableResponse{Success: false, Message: err.Error()}, nil
				}

				// Save updated table state (async)
				s.saveTableStateAsync(req.TableId, "host transferred")

				return &pokerrpc.LeaveTableResponse{
					Success: true,
					Message: fmt.Sprintf("Successfully left table. Host transferred to %s", newHostID),
				}, nil
			}
		}

		// If no other players remain, close the table
		delete(s.tables, req.TableId)
		err = s.db.DeleteTableState(req.TableId)
		if err != nil {
			s.log.Errorf("Failed to delete table state from database: %v", err)
		}

		return &pokerrpc.LeaveTableResponse{
			Success: true,
			Message: "Host left - table closed (no other players)",
		}, nil
	}

	// Save updated table state (async)
	s.saveTableStateAsync(req.TableId, "player left")

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

	// Save game state after bet (async)
	s.saveTableStateAsync(req.TableId, "bet made")

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

	// Save game state after fold (async)
	s.saveTableStateAsync(req.TableId, "player folded")

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

	// Save game state after call (async)
	s.saveTableStateAsync(req.TableId, "player called")

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

	// Save game state after check (async)
	s.saveTableStateAsync(req.TableId, "player checked")

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

// Game state persistence methods

// saveTableState persists the current table state to the database
func (s *Server) saveTableState(tableID string) error {
	s.mu.RLock()
	table, ok := s.tables[tableID]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("table not found")
	}

	// Convert table state to database format
	config := table.GetConfig()
	game := table.GetGame()

	dbTableState := &db.TableState{
		ID:            config.ID,
		HostID:        config.HostID,
		BuyIn:         config.BuyIn,
		MinPlayers:    config.MinPlayers,
		MaxPlayers:    config.MaxPlayers,
		SmallBlind:    config.SmallBlind,
		BigBlind:      config.BigBlind,
		MinBalance:    config.MinBalance,
		StartingChips: config.StartingChips,
		GameStarted:   table.IsGameStarted(),
		GamePhase:     table.GetGamePhase().String(),
		CreatedAt:     "", // Will be set by database
		LastAction:    "", // Will be set by database
	}

	// Add game-specific state if game exists
	if game != nil {
		dbTableState.Dealer = game.GetDealer()
		dbTableState.CurrentPlayer = game.GetCurrentPlayer()
		dbTableState.CurrentBet = game.GetCurrentBet()
		dbTableState.Pot = game.GetPot()
		dbTableState.Round = game.GetRound()
		dbTableState.BetRound = game.GetBetRound()
		dbTableState.CommunityCards = game.GetCommunityCards()
		dbTableState.DeckState = game.GetDeckState()
	}

	// Save table state
	err := s.db.SaveTableState(dbTableState)
	if err != nil {
		return fmt.Errorf("failed to save table state: %v", err)
	}

	// Save user states (as player states in database)
	for _, user := range table.GetUsers() {
		err := s.saveUserAsPlayerState(tableID, user)
		if err != nil {
			s.log.Errorf("Failed to save user state for %s: %v", user.ID, err)
		}
	}

	// Save active game player states if game is running
	if game != nil {
		for _, player := range game.GetPlayers() {
			err := s.savePlayerState(tableID, player)
			if err != nil {
				s.log.Errorf("Failed to save player state for %s: %v", player.ID, err)
			}
		}
	}

	return nil
}

// saveUserAsPlayerState converts a User to PlayerState for database storage
func (s *Server) saveUserAsPlayerState(tableID string, user *poker.User) error {
	dbPlayerState := &db.PlayerState{
		PlayerID:        user.ID,
		TableID:         tableID,
		TableSeat:       user.TableSeat,
		IsReady:         user.IsReady,
		IsDisconnected:  false, // Will be set separately when player disconnects
		LastAction:      "",    // Will be set by database
		Balance:         0,     // No game chips when just seated at table
		StartingBalance: 0,     // No starting balance until game starts
		HasBet:          user.HasBet,
		HasFolded:       user.HasFolded,
		IsAllIn:         false,
		IsDealer:        false,
		IsTurn:          false,
		GameState:       "AT_TABLE",
		Hand:            user.Hand,
		HandDescription: "",
	}

	return s.db.SavePlayerState(tableID, dbPlayerState)
}

// savePlayerState persists a player's state to the database (for active game players)
func (s *Server) savePlayerState(tableID string, player *poker.Player) error {
	dbPlayerState := &db.PlayerState{
		PlayerID:        player.ID,
		TableID:         tableID,
		TableSeat:       player.TableSeat,
		IsReady:         player.IsReady,
		IsDisconnected:  false, // Will be set separately when player disconnects
		LastAction:      "",    // Will be set by database
		Balance:         player.Balance,
		StartingBalance: player.StartingBalance,
		HasBet:          player.HasBet,
		HasFolded:       player.HasFolded,
		IsAllIn:         player.IsAllIn,
		IsDealer:        player.IsDealer,
		IsTurn:          player.IsTurn,
		GameState:       player.GetGameState(),
		Hand:            player.Hand,
		HandDescription: player.HandDescription,
	}

	return s.db.SavePlayerState(tableID, dbPlayerState)
}

// loadTableFromDatabase restores a table from the database
func (s *Server) loadTableFromDatabase(tableID string) (*poker.Table, error) {
	// Load table state
	dbTableState, err := s.db.LoadTableState(tableID)
	if err != nil {
		return nil, fmt.Errorf("failed to load table state: %v", err)
	}

	// Create table config
	cfg := poker.TableConfig{
		ID:             dbTableState.ID,
		Log:            s.logBackend.Logger("TABLE"),
		HostID:         dbTableState.HostID,
		BuyIn:          dbTableState.BuyIn,
		MinPlayers:     dbTableState.MinPlayers,
		MaxPlayers:     dbTableState.MaxPlayers,
		SmallBlind:     dbTableState.SmallBlind,
		BigBlind:       dbTableState.BigBlind,
		MinBalance:     dbTableState.MinBalance,
		StartingChips:  dbTableState.StartingChips,
		TimeBank:       30 * time.Second, // Default
		AutoStartDelay: 3 * time.Second,  // Default
	}

	// Create table
	table := poker.NewTable(cfg)
	table.SetNotificationSender(s)
	table.SetStateSaver(s)

	// Load player states
	dbPlayerStates, err := s.db.LoadPlayerStates(tableID)
	if err != nil {
		return nil, fmt.Errorf("failed to load player states: %v", err)
	}

	// Restore users to table
	for _, dbPlayerState := range dbPlayerStates {
		user := s.restoreUserFromState(dbPlayerState)

		// Add user back to table
		_, err := table.AddNewUser(user.ID, user.ID, user.AccountBalance, user.TableSeat)
		if err != nil {
			s.log.Errorf("Failed to add restored user %s to table: %v", user.ID, err)
			continue
		}

		// Update user state from saved data
		restoredUser := table.GetUser(user.ID)
		if restoredUser != nil {
			s.applyUserState(restoredUser, dbPlayerState)
		}
	}

	// Restore game state if game was started
	if dbTableState.GameStarted {
		err := s.restoreGameState(table, dbTableState, dbPlayerStates)
		if err != nil {
			s.log.Errorf("Failed to restore game state for table %s: %v", tableID, err)
		} else {
			s.log.Infof("Successfully restored active game for table %s", tableID)
		}
	}

	return table, nil
}

// restoreUserFromState creates a user from saved state
func (s *Server) restoreUserFromState(dbPlayerState *db.PlayerState) *poker.User {
	// Get the player's current DCR balance from the database
	dcrBalance, err := s.db.GetPlayerBalance(dbPlayerState.PlayerID)
	if err != nil {
		s.log.Errorf("Failed to get DCR balance for player %s: %v", dbPlayerState.PlayerID, err)
		dcrBalance = 0 // Default to 0 if we can't get the balance
	}

	user := poker.NewUser(dbPlayerState.PlayerID, dbPlayerState.PlayerID, dcrBalance, dbPlayerState.TableSeat)
	s.applyUserState(user, dbPlayerState)
	return user
}

// applyUserState applies saved state to a user
func (s *Server) applyUserState(user *poker.User, dbPlayerState *db.PlayerState) {
	user.TableSeat = dbPlayerState.TableSeat
	user.IsReady = dbPlayerState.IsReady
	user.HasBet = dbPlayerState.HasBet
	user.HasFolded = dbPlayerState.HasFolded
	user.SetGameState(dbPlayerState.GameState)

	// Restore hand if it exists
	if dbPlayerState.Hand != nil {
		// The hand was stored as JSON string, convert it back to []Card
		if handJSON, ok := dbPlayerState.Hand.(string); ok && handJSON != "" && handJSON != "[]" {
			var cards []poker.Card
			if err := json.Unmarshal([]byte(handJSON), &cards); err == nil {
				user.Hand = cards
			}
		}
	}
}

// transferTableHost transfers host ownership to a new user
func (s *Server) transferTableHost(tableID, newHostID string) error {
	table, ok := s.tables[tableID]
	if !ok {
		return fmt.Errorf("table not found")
	}

	// Use the table's SetHost method to transfer ownership
	err := table.SetHost(newHostID)
	if err != nil {
		return fmt.Errorf("failed to transfer host: %v", err)
	}

	s.log.Infof("Host transferred to %s for table %s", newHostID, tableID)

	return nil
}

// restoreGameState restores an active game from database state
func (s *Server) restoreGameState(table *poker.Table, dbTableState *db.TableState, dbPlayerStates []*db.PlayerState) error {
	// Start the game to initialize it
	err := table.StartGame()
	if err != nil {
		return fmt.Errorf("failed to start game: %v", err)
	}

	game := table.GetGame()
	if game == nil {
		return fmt.Errorf("game not created")
	}

	// Restore game-level state
	game.SetGameState(
		dbTableState.Dealer,
		dbTableState.CurrentPlayer,
		dbTableState.Round,
		dbTableState.BetRound,
		dbTableState.CurrentBet,
		dbTableState.Pot,
		s.parseGamePhase(dbTableState.GamePhase),
	)

	// Restore community cards
	if dbTableState.CommunityCards != nil {
		if communityCardsJSON, ok := dbTableState.CommunityCards.(string); ok && communityCardsJSON != "" && communityCardsJSON != "[]" {
			var communityCards []poker.Card
			if err := json.Unmarshal([]byte(communityCardsJSON), &communityCards); err == nil {
				game.SetCommunityCards(communityCards)
			}
		}
	}

	// Restore deck state
	if dbTableState.DeckState != nil {
		if deckStateJSON, ok := dbTableState.DeckState.(string); ok && deckStateJSON != "" && deckStateJSON != "{}" {
			var deckState poker.DeckState
			if err := json.Unmarshal([]byte(deckStateJSON), &deckState); err == nil {
				// Create a new RNG for the restored deck
				rng := rand.New(rand.NewSource(time.Now().UnixNano()))
				restoredDeck, err := poker.NewDeckFromState(&deckState, rng)
				if err == nil {
					// Replace the game's deck (this would require adding a method to Game)
					s.log.Debugf("Restored deck with %d remaining cards", restoredDeck.Size())
				}
			}
		}
	}

	// Restore active game players (those with game state beyond AT_TABLE)
	for _, dbPlayerState := range dbPlayerStates {
		if dbPlayerState.GameState != "AT_TABLE" {
			// Find the corresponding game player
			for _, player := range game.GetPlayers() {
				if player.ID == dbPlayerState.PlayerID {
					// Restore player's game state
					player.Balance = dbPlayerState.Balance
					player.StartingBalance = dbPlayerState.StartingBalance
					player.HasBet = dbPlayerState.HasBet
					player.HasFolded = dbPlayerState.HasFolded
					player.IsAllIn = dbPlayerState.IsAllIn
					player.IsDealer = dbPlayerState.IsDealer
					player.IsTurn = dbPlayerState.IsTurn
					player.HandDescription = dbPlayerState.HandDescription
					player.SetGameState(dbPlayerState.GameState)

					// Restore player's hand
					if dbPlayerState.Hand != nil {
						if handJSON, ok := dbPlayerState.Hand.(string); ok && handJSON != "" && handJSON != "[]" {
							var cards []poker.Card
							if err := json.Unmarshal([]byte(handJSON), &cards); err == nil {
								player.Hand = cards
							}
						}
					}
					break
				}
			}
		}
	}

	s.log.Infof("Restored game state: dealer=%d, currentPlayer=%d, pot=%d, phase=%s",
		dbTableState.Dealer, dbTableState.CurrentPlayer, dbTableState.Pot, dbTableState.GamePhase)

	return nil
}

// parseGamePhase converts a string game phase to the enum type
func (s *Server) parseGamePhase(phaseStr string) pokerrpc.GamePhase {
	switch phaseStr {
	case "WAITING":
		return pokerrpc.GamePhase_WAITING
	case "NEW_HAND_DEALING":
		return pokerrpc.GamePhase_NEW_HAND_DEALING
	case "PRE_FLOP":
		return pokerrpc.GamePhase_PRE_FLOP
	case "FLOP":
		return pokerrpc.GamePhase_FLOP
	case "TURN":
		return pokerrpc.GamePhase_TURN
	case "RIVER":
		return pokerrpc.GamePhase_RIVER
	case "SHOWDOWN":
		return pokerrpc.GamePhase_SHOWDOWN
	default:
		return pokerrpc.GamePhase_WAITING
	}
}

// markPlayerDisconnected marks a player as disconnected but keeps them in the game
func (s *Server) markPlayerDisconnected(tableID, playerID string) error {
	// Save current game state first
	err := s.saveTableState(tableID)
	if err != nil {
		s.log.Errorf("Failed to save table state when player %s disconnected: %v", playerID, err)
	}

	// Mark player as disconnected in database
	err = s.db.SetPlayerDisconnected(tableID, playerID)
	if err != nil {
		return fmt.Errorf("failed to mark player as disconnected: %v", err)
	}

	s.log.Infof("Player %s marked as disconnected from table %s", playerID, tableID)
	return nil
}

// markPlayerConnected marks a player as connected
func (s *Server) markPlayerConnected(tableID, playerID string) error {
	err := s.db.SetPlayerConnected(tableID, playerID)
	if err != nil {
		return fmt.Errorf("failed to mark player as connected: %v", err)
	}

	s.log.Infof("Player %s marked as connected to table %s", playerID, tableID)
	return nil
}

// loadAllTables loads all persisted tables from the database on server startup
func (s *Server) loadAllTables() error {
	s.log.Infof("Loading persisted tables from database...")

	// Get all table IDs from the database
	tableIDs, err := s.db.GetAllTableIDs()
	if err != nil {
		return fmt.Errorf("failed to get table IDs from database: %v", err)
	}

	if len(tableIDs) == 0 {
		s.log.Infof("No persisted tables found in database")
		return nil
	}

	loadedCount := 0
	for _, tableID := range tableIDs {
		table, err := s.loadTableFromDatabase(tableID)
		if err != nil {
			s.log.Errorf("Failed to load table %s: %v", tableID, err)
			continue
		}

		s.mu.Lock()
		s.tables[tableID] = table
		s.mu.Unlock()

		loadedCount++
		s.log.Infof("Loaded table %s from database", tableID)
	}

	s.log.Infof("Successfully loaded %d of %d persisted tables", loadedCount, len(tableIDs))
	return nil
}

// cleanupDisconnectedPlayers removes players who have been disconnected too long or have no chips
func (s *Server) cleanupDisconnectedPlayers() {
	// This should be called periodically to clean up placeholder players
	// TODO: Implement cleanup logic based on:
	// 1. Time since disconnection
	// 2. Player chip count (remove if 0 chips)
	// 3. Table activity
}

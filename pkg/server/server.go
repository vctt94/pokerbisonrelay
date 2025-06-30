package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort" // ensure deterministic ordering when (de)serializing player slices
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

	// Table state saving synchronization
	saveMutexes map[string]*sync.Mutex // tableID -> mutex for that table's saves
	saveMu      sync.RWMutex           // protects saveMutexes map

	// WaitGroup to ensure all async save goroutines complete before Shutdown
	saveWg sync.WaitGroup

	// Event-driven architecture components
	eventProcessor *EventProcessor
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
		saveMutexes:         make(map[string]*sync.Mutex),
	}

	// Initialize event processor for deadlock-free architecture
	server.eventProcessor = NewEventProcessor(server, 1000, 3) // queue size: 1000, workers: 3
	server.eventProcessor.Start()

	// Load persisted tables on startup
	err := server.loadAllTables()
	if err != nil {
		server.log.Errorf("Failed to load persisted tables: %v", err)
	}

	return server
}

// Stop gracefully stops the server
func (s *Server) Stop() {
	if s.eventProcessor != nil {
		s.eventProcessor.Stop()
	}
	// Wait for any in-flight asynchronous saves to complete before returning.
	s.saveWg.Wait()
}

// saveTableStateAsync saves table state asynchronously to avoid blocking game operations
func (s *Server) saveTableStateAsync(tableID string, reason string) {
	// Get or create a mutex for this table
	s.saveMu.Lock()
	saveMutex, exists := s.saveMutexes[tableID]
	if !exists {
		saveMutex = &sync.Mutex{}
		s.saveMutexes[tableID] = saveMutex
	}
	s.saveMu.Unlock()

	// Increment the WaitGroup to track this goroutine
	s.saveWg.Add(1)

	go func() {
		// Ensure the WaitGroup is decremented upon completion
		defer s.saveWg.Done()
		// Acquire the table-specific mutex to serialize saves for this table
		saveMutex.Lock()
		defer saveMutex.Unlock()

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

	// Create table
	table := poker.NewTable(cfg)
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

	// Now that the table is fully initialized and the creator seated, register it.
	s.tables[cfg.ID] = table

	// Table already added to map above; no further action needed here.

	return &pokerrpc.CreateTableResponse{TableId: cfg.ID}, nil
}

func (s *Server) JoinTable(ctx context.Context, req *pokerrpc.JoinTableRequest) (*pokerrpc.JoinTableResponse, error) {
	table, ok := s.getTable(req.TableId)
	if !ok {
		return &pokerrpc.JoinTableResponse{Success: false, Message: "Table not found"}, nil
	}

	config := table.GetConfig()

	// Reconnection path – player already seated.
	if existingUser := table.GetUser(req.PlayerId); existingUser != nil {
		evt, err := CollectGameEventSnapshot(GameEventTypePlayerJoined, s, req.TableId, req.PlayerId, 0, map[string]interface{}{
			"message": fmt.Sprintf("Player %s rejoined the table", req.PlayerId),
		})
		if err != nil {
			s.log.Errorf("Failed to collect event snapshot: %v", err)
		} else {
			s.eventProcessor.PublishEvent(evt)
		}

		return &pokerrpc.JoinTableResponse{
			Success:    true,
			Message:    fmt.Sprintf("Reconnected to table. You have %d DCR balance.", existingUser.DCRAccountBalance),
			NewBalance: 0,
		}, nil
	}

	// New player joining – verify balance.
	dcrBalance, err := s.db.GetPlayerBalance(req.PlayerId)
	if err != nil {
		return nil, err
	}
	if dcrBalance < config.BuyIn {
		return &pokerrpc.JoinTableResponse{Success: false, Message: "Insufficient DCR balance for buy-in"}, nil
	}

	// Determine next free seat.
	occupied := make(map[int]bool)
	for _, u := range table.GetUsers() {
		occupied[u.TableSeat] = true
	}
	seat := 0
	for i := 0; i < config.MaxPlayers; i++ {
		if !occupied[i] {
			seat = i
			break
		}
	}

	// Add user to table.
	newUser, err := table.AddNewUser(req.PlayerId, req.PlayerId, dcrBalance, seat)
	if err != nil {
		return &pokerrpc.JoinTableResponse{Success: false, Message: err.Error()}, nil
	}

	// Deduct buy-in.
	if err := s.db.UpdatePlayerBalance(req.PlayerId, -config.BuyIn, "table buy-in", "joined table"); err != nil {
		table.RemoveUser(req.PlayerId)
		return nil, err
	}
	// Update player's on-table DCR balance atomically to avoid data races with concurrent snapshots.
	_ = table.SetUserDCRAccountBalance(req.PlayerId, dcrBalance-config.BuyIn)

	// Persist player state.
	if err := s.saveUserAsPlayerState(req.TableId, newUser); err != nil {
		s.log.Errorf("Failed to save new player state: %v", err)
	}

	// Publish async event snapshot.
	if evt, err := CollectGameEventSnapshot(GameEventTypePlayerJoined, s, req.TableId, req.PlayerId, 0, map[string]interface{}{
		"message": fmt.Sprintf("Player %s joined the table", req.PlayerId),
	}); err == nil {
		s.eventProcessor.PublishEvent(evt)
	} else {
		s.log.Errorf("Failed to collect event snapshot: %v", err)
	}

	return &pokerrpc.JoinTableResponse{
		Success:    true,
		Message:    "Successfully joined table",
		NewBalance: newUser.DCRAccountBalance,
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
		// Directly mark as disconnected while holding the existing server lock to
		// avoid re-deadlocking by acquiring it a second time inside
		// markPlayerDisconnected().
		user.IsDisconnected = true

		// Persist the table snapshot asynchronously after mutating the in-memory
		// state.
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

		// Clean up the save mutex for this table
		s.saveMu.Lock()
		delete(s.saveMutexes, req.TableId)
		s.saveMu.Unlock()

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
	// Get table references with server lock
	s.mu.RLock()
	tableRefs := make([]*poker.Table, 0, len(s.tables))
	for _, table := range s.tables {
		tableRefs = append(tableRefs, table)
	}
	s.mu.RUnlock()

	// Build response using regular table methods (no server lock held)
	tables := make([]*pokerrpc.Table, 0, len(tableRefs))
	for _, table := range tableRefs {
		config := table.GetConfig()
		users := table.GetUsers()
		game := table.GetGame()

		protoTable := &pokerrpc.Table{
			Id:              config.ID,
			HostId:          config.HostID,
			SmallBlind:      config.SmallBlind,
			BigBlind:        config.BigBlind,
			MaxPlayers:      int32(table.GetMaxPlayers()),
			MinPlayers:      int32(table.GetMinPlayers()),
			CurrentPlayers:  int32(len(users)),
			MinBalance:      config.MinBalance,
			BuyIn:           config.BuyIn,
			GameStarted:     game != nil,
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
	// First acquire server lock to get table reference
	s.mu.RLock()
	table, ok := s.tables[req.TableId]
	s.mu.RUnlock()

	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	// Use table method to set player ready - table handles its own locking
	// Following lock hierarchy: Server → Table (no server lock held during table operation)
	err := table.SetPlayerReady(req.PlayerId, true)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	allReady := table.CheckAllPlayersReady()
	gameStarted := table.IsGameStarted()

	// Collect snapshot and publish event for async processing
	event, err := CollectGameEventSnapshot(GameEventTypePlayerReady, s, req.TableId, req.PlayerId, 0, map[string]interface{}{
		"message":     fmt.Sprintf("Player %s is ready", req.PlayerId),
		"allReady":    allReady,
		"gameStarted": gameStarted,
	})
	if err != nil {
		s.log.Errorf("Failed to collect event snapshot: %v", err)
	} else {
		s.eventProcessor.PublishEvent(event)
	}

	// If all players are ready and the game hasn't started yet, start the game
	if allReady && !gameStarted {
		if errStart := table.StartGame(); errStart != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to start game: %v", errStart))
		}

		// Collect and publish a dedicated GAME_STARTED event *after* the game has been
		// successfully created so that the emitted snapshot reflects the brand-new
		// game state (dealer, blinds, current player, etc.). Without this, the first
		// game update received by the clients would still be in the pre-start state
		// which prevents the UI from progressing to the actual hand.
		gameStartedEvent, errGS := CollectGameEventSnapshot(GameEventTypeGameStarted, s, req.TableId, req.PlayerId, 0, map[string]interface{}{
			"message": fmt.Sprintf("Game started on table %s", req.TableId),
		})
		if errGS != nil {
			s.log.Errorf("Failed to collect GAME_STARTED event snapshot: %v", errGS)
		} else {
			s.eventProcessor.PublishEvent(gameStartedEvent)
		}

		// Attach callback to broadcast NEW_HAND_STARTED events triggered by auto-start logic
		if g := table.GetGame(); g != nil {
			g.SetOnNewHandStartedCallback(func() {
				// Build and publish snapshot
				evt, err := CollectGameEventSnapshot(GameEventTypeNewHandStarted, s, req.TableId, "", 0, map[string]interface{}{
					"message": fmt.Sprintf("New hand started on table %s", req.TableId),
				})
				if err == nil {
					s.eventProcessor.PublishEvent(evt)
				} else {
					s.log.Errorf("Failed to collect NEW_HAND_STARTED event snapshot: %v", err)
				}
			})
		}
	}

	return &pokerrpc.SetPlayerReadyResponse{
		Success:         true,
		Message:         "Player is ready",
		AllPlayersReady: allReady,
	}, nil
}

func (s *Server) SetPlayerUnready(ctx context.Context, req *pokerrpc.SetPlayerUnreadyRequest) (*pokerrpc.SetPlayerUnreadyResponse, error) {
	// First acquire server lock to get table reference
	s.mu.RLock()
	table, ok := s.tables[req.TableId]
	s.mu.RUnlock()

	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	// Use table method to set player unready - table handles its own locking
	// Following lock hierarchy: Server → Table (no server lock held during table operation)
	err := table.SetPlayerReady(req.PlayerId, false)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	// Collect snapshot and publish event for async processing
	event, err := CollectGameEventSnapshot(GameEventTypePlayerReady, s, req.TableId, req.PlayerId, 0, map[string]interface{}{
		"message": fmt.Sprintf("Player %s is unready", req.PlayerId),
		"ready":   false,
	})
	if err != nil {
		s.log.Errorf("Failed to collect event snapshot: %v", err)
	} else {
		s.eventProcessor.PublishEvent(event)
	}

	return &pokerrpc.SetPlayerUnreadyResponse{
		Success: true,
		Message: "Player is unready",
	}, nil
}

func (s *Server) GetPlayerCurrentTable(ctx context.Context, req *pokerrpc.GetPlayerCurrentTableRequest) (*pokerrpc.GetPlayerCurrentTableResponse, error) {
	// Get table references with server lock
	s.mu.RLock()
	tableRefs := make([]*poker.Table, 0, len(s.tables))
	for _, table := range s.tables {
		tableRefs = append(tableRefs, table)
	}
	s.mu.RUnlock()

	// Search through tables using regular methods (no server lock held)
	for _, table := range tableRefs {
		if table.GetUser(req.PlayerId) != nil {
			config := table.GetConfig()
			return &pokerrpc.GetPlayerCurrentTableResponse{
				TableId: config.ID,
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
				Id:      user.ID,
				Balance: 0, // No poker chips when no game - Balance field should be poker chips, not DCR
				IsReady: user.IsReady,

				Hand: make([]*pokerrpc.Card, 0), // Empty hand when no game
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
	table, ok := s.getTable(req.TableId)
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	if !table.IsGameStarted() {
		return nil, status.Error(codes.FailedPrecondition, "game not started")
	}

	if table.GetCurrentPlayerID() != req.PlayerId {
		return nil, status.Error(codes.FailedPrecondition, "not your turn")
	}

	if err := table.MakeBet(req.PlayerId, req.Amount); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	balance, err := s.db.GetPlayerBalance(req.PlayerId)
	if err != nil {
		return nil, err
	}

	if evt, err := CollectGameEventSnapshot(GameEventTypeBetMade, s, req.TableId, req.PlayerId, req.Amount, map[string]interface{}{
		"message": fmt.Sprintf("Player %s bet %d chips", req.PlayerId, req.Amount),
	}); err == nil {
		s.eventProcessor.PublishEvent(evt)
	} else {
		s.log.Errorf("Failed to collect event snapshot: %v", err)
	}

	return &pokerrpc.MakeBetResponse{
		Success:    true,
		Message:    "Bet placed successfully",
		NewBalance: balance,
	}, nil
}

func (s *Server) Fold(ctx context.Context, req *pokerrpc.FoldRequest) (*pokerrpc.FoldResponse, error) {
	table, ok := s.getTable(req.TableId)
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}
	if !table.IsGameStarted() {
		return nil, status.Error(codes.FailedPrecondition, "game not started")
	}
	if table.GetCurrentPlayerID() != req.PlayerId {
		return nil, status.Error(codes.FailedPrecondition, "not your turn")
	}
	if err := table.HandleFold(req.PlayerId); err != nil {
		return nil, status.Error(codes.Internal, "failed to process fold: "+err.Error())
	}

	if evt, err := CollectGameEventSnapshot(GameEventTypePlayerFolded, s, req.TableId, req.PlayerId, 0, map[string]interface{}{
		"message": fmt.Sprintf("Player %s folded", req.PlayerId),
	}); err == nil {
		s.eventProcessor.PublishEvent(evt)
	} else {
		s.log.Errorf("Failed to collect event snapshot: %v", err)
	}

	return &pokerrpc.FoldResponse{Success: true, Message: "Folded successfully"}, nil
}

// Call implements the Call RPC method
func (s *Server) Call(ctx context.Context, req *pokerrpc.CallRequest) (*pokerrpc.CallResponse, error) {
	table, ok := s.getTable(req.TableId)
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}
	if !table.IsGameStarted() {
		return nil, status.Error(codes.FailedPrecondition, "game not started")
	}
	if table.GetCurrentPlayerID() != req.PlayerId {
		return nil, status.Error(codes.FailedPrecondition, "not your turn")
	}

	// Determine how many chips the player actually needs to add (delta) to call.
	var prevBet int64 = 0
	if game := table.GetGame(); game != nil {
		for _, p := range game.GetPlayers() {
			if p.ID == req.PlayerId {
				prevBet = p.HasBet
				break
			}
		}
	}
	// Capture current bet before performing the call so we know the target amount.
	currentBet := table.GetCurrentBet()

	if err := table.HandleCall(req.PlayerId); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	// Calculate delta based on player's previous bet.
	delta := currentBet - prevBet
	if delta < 0 {
		delta = 0 // safety — shouldn't happen
	}

	if evt, err := CollectGameEventSnapshot(GameEventTypeCallMade, s, req.TableId, req.PlayerId, delta, map[string]interface{}{
		"message": fmt.Sprintf("Player %s called %d chips", req.PlayerId, delta),
	}); err == nil {
		s.eventProcessor.PublishEvent(evt)
	} else {
		s.log.Errorf("Failed to collect event snapshot: %v", err)
	}

	return &pokerrpc.CallResponse{Success: true, Message: "Call successful"}, nil
}

// Check implements the Check RPC method
func (s *Server) Check(ctx context.Context, req *pokerrpc.CheckRequest) (*pokerrpc.CheckResponse, error) {
	table, ok := s.getTable(req.TableId)
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}
	if !table.IsGameStarted() {
		return nil, status.Error(codes.FailedPrecondition, "game not started")
	}
	if table.GetCurrentPlayerID() != req.PlayerId {
		return nil, status.Error(codes.FailedPrecondition, "not your turn")
	}

	if err := table.HandleCheck(req.PlayerId); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	if evt, err := CollectGameEventSnapshot(GameEventTypeCheckMade, s, req.TableId, req.PlayerId, 0, map[string]interface{}{
		"message": fmt.Sprintf("Player %s checked", req.PlayerId),
	}); err == nil {
		s.eventProcessor.PublishEvent(evt)
	} else {
		s.log.Errorf("Failed to collect event snapshot: %v", err)
	}

	return &pokerrpc.CheckResponse{Success: true, Message: "Check successful"}, nil
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
	if !ok {
		s.mu.RUnlock()
		return fmt.Errorf("table not found")
	}
	s.mu.RUnlock()

	// Get atomic snapshot of table state to prevent race conditions
	tableSnapshot := table.GetStateSnapshot()

	// Create table state for database
	dbTableState := &db.TableState{
		ID:            tableID,
		HostID:        tableSnapshot.Config.HostID,
		BuyIn:         tableSnapshot.Config.BuyIn,
		MinPlayers:    tableSnapshot.Config.MinPlayers,
		MaxPlayers:    tableSnapshot.Config.MaxPlayers,
		SmallBlind:    tableSnapshot.Config.SmallBlind,
		BigBlind:      tableSnapshot.Config.BigBlind,
		MinBalance:    tableSnapshot.Config.MinBalance,
		StartingChips: tableSnapshot.Config.StartingChips,
		GameStarted:   tableSnapshot.GameStarted,
		GamePhase:     tableSnapshot.GamePhase.String(),
		CreatedAt:     "", // Will be set by database
		LastAction:    "", // Will be set by database
	}

	// Add game-specific state if game exists
	if tableSnapshot.Game != nil {
		dbTableState.Dealer = tableSnapshot.Game.Dealer
		dbTableState.CurrentPlayer = tableSnapshot.Game.CurrentPlayer
		dbTableState.CurrentBet = tableSnapshot.Game.CurrentBet
		dbTableState.Pot = tableSnapshot.Game.Pot
		dbTableState.Round = tableSnapshot.Game.Round
		dbTableState.BetRound = tableSnapshot.Game.BetRound
		dbTableState.CommunityCards = tableSnapshot.Game.CommunityCards
		dbTableState.DeckState = tableSnapshot.Game.DeckState
	}

	// Build an aggregated, de-duplicated set of player states.
	playerStateMap := make(map[string]*db.PlayerState)

	// First gather the table users (these include players that might not yet be seated in a running game).
	for _, user := range tableSnapshot.Users {
		ps := &db.PlayerState{
			PlayerID:        user.ID,
			TableID:         tableID,
			TableSeat:       user.TableSeat,
			IsReady:         user.IsReady,
			Balance:         0,
			StartingBalance: 0,
			GameState:       "AT_TABLE",
		}
		playerStateMap[user.ID] = ps
	}

	// Then override/add any players that are part of the active game so that their in-game state is captured.
	if tableSnapshot.Game != nil {
		for _, player := range tableSnapshot.Game.Players {
			ps := &db.PlayerState{
				PlayerID:        player.ID,
				TableID:         tableID,
				TableSeat:       player.TableSeat,
				IsReady:         player.IsReady,
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
			playerStateMap[player.ID] = ps
		}
	}

	// Convert map to slice for saving, but ensure deterministic ordering by table seat so
	// that the saved CurrentPlayer index will correctly map back to the same player
	// after restoration regardless of map iteration order.
	aggregatedPlayerStates := make([]*db.PlayerState, 0, len(playerStateMap))
	for _, ps := range playerStateMap {
		aggregatedPlayerStates = append(aggregatedPlayerStates, ps)
	}

	// Sort by TableSeat to guarantee stable order across save/load cycles.
	sort.Slice(aggregatedPlayerStates, func(i, j int) bool {
		return aggregatedPlayerStates[i].TableSeat < aggregatedPlayerStates[j].TableSeat
	})

	// Persist the whole snapshot atomically.
	if err := s.db.SaveSnapshot(dbTableState, aggregatedPlayerStates); err != nil {
		return fmt.Errorf("failed to save table snapshot: %v", err)
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
		LastAction:      "", // Will be set by database
		Balance:         0,  // No game chips when just seated at table
		StartingBalance: 0,  // No starting balance until game starts

		IsAllIn:         false,
		IsDealer:        false,
		IsTurn:          false,
		GameState:       "AT_TABLE",
		HandDescription: "",
	}

	return s.db.SavePlayerState(tableID, dbPlayerState)
}

// saveUserSnapshotAsPlayerState converts a user snapshot to PlayerState for database storage
func (s *Server) saveUserSnapshotAsPlayerState(tableID string, snapshot struct {
	ID                string
	TableSeat         int
	IsReady           bool
	DCRAccountBalance int64
}) error {
	dbPlayerState := &db.PlayerState{
		PlayerID:        snapshot.ID,
		TableID:         tableID,
		TableSeat:       snapshot.TableSeat,
		IsReady:         snapshot.IsReady,
		LastAction:      "", // Will be set by database
		Balance:         0,  // No game chips when just seated at table
		StartingBalance: 0,  // No starting balance until game starts

		IsAllIn:         false,
		IsDealer:        false,
		IsTurn:          false,
		GameState:       "AT_TABLE",
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
		LastAction:      "", // Will be set by database
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

// savePlayerSnapshotAsPlayerState persists a player snapshot to the database (for active game players)
func (s *Server) savePlayerSnapshotAsPlayerState(tableID string, snapshot struct {
	ID              string
	TableSeat       int
	IsReady         bool
	Balance         int64
	StartingBalance int64
	HasBet          int64
	HasFolded       bool
	IsAllIn         bool
	IsDealer        bool
	IsTurn          bool
	GameState       string
	Hand            []poker.Card
	HandDescription string
}) error {
	dbPlayerState := &db.PlayerState{
		PlayerID:        snapshot.ID,
		TableID:         tableID,
		TableSeat:       snapshot.TableSeat,
		IsReady:         snapshot.IsReady,
		LastAction:      "", // Will be set by database
		Balance:         snapshot.Balance,
		StartingBalance: snapshot.StartingBalance,
		HasBet:          snapshot.HasBet,
		HasFolded:       snapshot.HasFolded,
		IsAllIn:         snapshot.IsAllIn,
		IsDealer:        snapshot.IsDealer,
		IsTurn:          snapshot.IsTurn,
		GameState:       snapshot.GameState,
		Hand:            snapshot.Hand,
		HandDescription: snapshot.HandDescription,
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
	table.SetStateSaver(s)

	// Register the table early so that any asynchronous snapshot operations
	// triggered during restoration can successfully locate it.
	s.mu.Lock()
	s.tables[tableID] = table
	s.mu.Unlock()

	// Load player states
	dbPlayerStates, err := s.db.LoadPlayerStates(tableID)
	if err != nil {
		return nil, fmt.Errorf("failed to load player states: %v", err)
	}

	// Ensure deterministic order by sorting by seat before we recreate users. This guarantees
	// that the index-based CurrentPlayer value persisted in the snapshot correctly
	// references the same logical player once the game is restored.
	sort.Slice(dbPlayerStates, func(i, j int) bool {
		return dbPlayerStates[i].TableSeat < dbPlayerStates[j].TableSeat
	})

	// Restore users to table
	for _, dbPlayerState := range dbPlayerStates {
		user := s.restoreUserFromState(dbPlayerState)

		// Add user back to table
		_, err := table.AddNewUser(user.ID, user.ID, user.DCRAccountBalance, user.TableSeat)
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
	return user
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
	s.log.Infof("Restoring game state for table %s: phase=%s, dealer=%d, currentPlayer=%d",
		dbTableState.ID, dbTableState.GamePhase, dbTableState.Dealer, dbTableState.CurrentPlayer)

	// Build a fresh *poker.Game without triggering any hand setup logic. This
	// avoids posting blinds or dealing cards again during restoration.

	tblCfg := table.GetConfig()

	users := table.GetUsers()
	// Ensure stable ordering by seat so indices match persisted data.
	sort.Slice(users, func(i, j int) bool { return users[i].TableSeat < users[j].TableSeat })

	gCfg := poker.GameConfig{
		NumPlayers:     len(users),
		StartingChips:  tblCfg.StartingChips,
		SmallBlind:     tblCfg.SmallBlind,
		BigBlind:       tblCfg.BigBlind,
		AutoStartDelay: tblCfg.AutoStartDelay,
		Log:            s.logBackend.Logger("GAME"),
	}

	game, err := poker.NewGame(gCfg)
	if err != nil {
		return fmt.Errorf("failed to create game during restoration: %v", err)
	}

	// Populate game players from table users (creates fresh *Player objects).
	game.SetPlayers(users)

	// Inject the reconstructed game into the table (sets state to GAME_ACTIVE).
	table.RestoreGame(game)

	// Restore community cards
	if dbTableState.CommunityCards != nil {
		if communityCardsJSON, ok := dbTableState.CommunityCards.(string); ok && communityCardsJSON != "" && communityCardsJSON != "[]" {
			var communityCards []poker.Card
			if err := json.Unmarshal([]byte(communityCardsJSON), &communityCards); err == nil {
				game.SetCommunityCards(communityCards)
				s.log.Debugf("Restored %d community cards", len(communityCards))
			}
		}
	}

	// Restore game-level state using the SetGameState method
	gamePhase := s.parseGamePhase(dbTableState.GamePhase)
	game.SetGameState(
		dbTableState.Dealer,
		dbTableState.CurrentPlayer,
		dbTableState.Round,
		dbTableState.BetRound,
		dbTableState.CurrentBet,
		dbTableState.Pot,
		gamePhase,
	)

	// Restore player state from database, including hands
	game.ModifyPlayers(func(players []*poker.Player) {
		for _, dbPlayerState := range dbPlayerStates {
			for _, player := range players {
				if player.ID != dbPlayerState.PlayerID {
					continue
				}

				// Restore game state fields
				player.Balance = dbPlayerState.Balance
				player.StartingBalance = dbPlayerState.StartingBalance
				player.HasBet = dbPlayerState.HasBet
				player.HasFolded = dbPlayerState.HasFolded
				player.IsAllIn = dbPlayerState.IsAllIn
				player.IsDealer = dbPlayerState.IsDealer
				player.IsTurn = dbPlayerState.IsTurn
				player.HandDescription = dbPlayerState.HandDescription
				player.SetGameState(dbPlayerState.GameState)

				// Restore hand cards
				if dbPlayerState.Hand != nil {
					if handJSON, ok := dbPlayerState.Hand.(string); ok && handJSON != "" && handJSON != "[]" {
						var cards []poker.Card
						if err := json.Unmarshal([]byte(handJSON), &cards); err == nil {
							player.Hand = cards
							s.log.Debugf("Restored %d cards for player %s", len(cards), player.ID)
						} else {
							s.log.Errorf("Failed to unmarshal hand for player %s: %v", player.ID, err)
						}
					}
				}

				// Set table-level state
				player.TableSeat = dbPlayerState.TableSeat
				player.IsReady = dbPlayerState.IsReady

				s.log.Debugf("Restored player %s: balance=%d, hasbet=%d, folded=%v, disconnected=%v",
					player.ID, player.Balance, player.HasBet, player.HasFolded, player.IsDisconnected)

				break
			}
		}
	})

	// Reconstruct pot based on each player's saved bet so that GetPot() matches
	// the persisted total. We do this outside the ModifyPlayers block to avoid
	// holding the game write-lock for the additional potManager updates.
	for idx, p := range game.GetPlayers() {
		if p.HasBet > 0 {
			game.AddToPotForPlayer(idx, p.HasBet)
		}
	}

	// Ensure the pot total matches the snapshot exactly (bets alone may not
	// capture contributions from previous betting rounds).
	game.ForceSetPot(dbTableState.Pot)

	s.log.Infof("Successfully restored game state: dealer=%d, currentPlayer=%d, pot=%d, phase=%s, players=%d",
		dbTableState.Dealer, dbTableState.CurrentPlayer, dbTableState.Pot, dbTableState.GamePhase, len(game.GetPlayers()))

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
	s.mu.Lock()
	table, ok := s.tables[tableID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("table not found")
	}
	user := table.GetUser(playerID)
	if user == nil {
		s.mu.Unlock()
		return fmt.Errorf("player not found at table")
	}
	user.IsDisconnected = true
	s.mu.Unlock()

	// Persist table snapshot asynchronously so flag is saved in memory snapshot only.
	s.saveTableStateAsync(tableID, "player disconnected")
	s.log.Infof("Player %s marked as disconnected from table %s", playerID, tableID)
	return nil
}

// markPlayerConnected marks a player as connected
func (s *Server) markPlayerConnected(tableID, playerID string) error {
	s.mu.Lock()
	table, ok := s.tables[tableID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("table not found")
	}
	user := table.GetUser(playerID)
	if user == nil {
		s.mu.Unlock()
		return fmt.Errorf("player not found at table")
	}
	user.IsDisconnected = false
	s.mu.Unlock()

	s.saveTableStateAsync(tableID, "player reconnected")
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
	s.log.Debugf("Running disconnected player cleanup...")

	s.mu.RLock()
	tableIDs := make([]string, 0, len(s.tables))
	for tableID := range s.tables {
		tableIDs = append(tableIDs, tableID)
	}
	s.mu.RUnlock()

	for _, tableID := range tableIDs {
		s.cleanupDisconnectedPlayersForTable(tableID)
	}
}

// cleanupDisconnectedPlayersForTable cleans up disconnected players for a specific table
func (s *Server) cleanupDisconnectedPlayersForTable(tableID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	table, ok := s.tables[tableID]
	if !ok {
		return
	}

	playersToRemove := []string{}

	for _, user := range table.GetUsers() {
		if !user.IsDisconnected {
			continue
		}
		// Determine chip balance: if game active, look at game player; else 0 chips triggers removal.
		chipBalance := int64(0)
		if table.IsGameStarted() && table.GetGame() != nil {
			for _, gp := range table.GetGame().GetPlayers() {
				if gp.ID == user.ID {
					chipBalance = gp.Balance
					break
				}
			}
		}
		if chipBalance == 0 {
			playersToRemove = append(playersToRemove, user.ID)
			s.log.Infof("Marking disconnected player %s with 0 chips for removal", user.ID)
		}
		// TODO: time-based cleanup as before
	}

	for _, pid := range playersToRemove {
		_ = table.RemoveUser(pid)
		_ = s.db.DeletePlayerState(tableID, pid)
	}

	if len(playersToRemove) > 0 {
		s.saveTableStateAsync(tableID, "disconnected player cleanup")
	}
}

// applyUserState applies saved player state to a restored user
func (s *Server) applyUserState(user *poker.User, dbPlayerState *db.PlayerState) {
	// Apply table-level state
	user.IsReady = dbPlayerState.IsReady

	// Note: TableSeat should already be set correctly when user was created from state
	// but ensure it matches the saved state
	user.TableSeat = dbPlayerState.TableSeat

	s.log.Debugf("Applied user state for player %s: ready=%v, seat=%d",
		user.ID, user.IsReady, user.TableSeat)
}

// handlePlayerReconnection manages the state when a player reconnects
func (s *Server) handlePlayerReconnection(tableID, playerID string) error {
	s.mu.Lock()

	table, ok := s.tables[tableID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("table not found")
	}

	// Check if player exists in table
	user := table.GetUser(playerID)
	if user == nil {
		s.mu.Unlock()
		return fmt.Errorf("player not found at table")
	}

	// Directly mark as connected while holding the server lock to prevent a
	// nested lock deadlock.
	user.IsDisconnected = false

	// Persist the change so that the player's reconnected status survives server
	// restarts.
	s.saveTableStateAsync(tableID, "player reconnected")

	// Collect all data needed for async operations while holding lock
	playerIDs := s.getTablePlayerIDs(tableID)
	gameStates := s.buildGameStatesForAllPlayers(tableID)

	reconnectNotification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_PLAYER_READY,
		PlayerId: playerID,
		TableId:  tableID,
		Message:  fmt.Sprintf("Player %s has reconnected", playerID),
	}

	s.mu.Unlock() // Release lock before async operations

	// Async operations using lock-free methods
	go func() {
		s.notifyPlayers(playerIDs, reconnectNotification)
		if gameStates != nil {
			s.sendGameStateUpdates(tableID, gameStates)
		}
	}()

	s.log.Infof("Player %s successfully reconnected to table %s", playerID, tableID)
	return nil
}

// handlePlayerReconnectionInternal manages the state when a player reconnects (called from notification queue)
func (s *Server) handlePlayerReconnectionInternal(tableID, playerID string) error {
	// Mark player as connected in database
	err := s.markPlayerConnected(tableID, playerID)
	if err != nil {
		s.log.Errorf("Failed to mark player as connected: %v", err)
	}

	// Save current state to ensure reconnection is persisted
	s.saveTableStateAsync(tableID, "player reconnected")

	// Build notification data (using helper methods that handle their own locks)
	reconnectNotification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_PLAYER_READY,
		PlayerId: playerID,
		TableId:  tableID,
		Message:  fmt.Sprintf("Player %s has reconnected", playerID),
	}

	// Get player IDs (this method handles its own locks)
	playerIDs := s.getTablePlayerIDs(tableID)

	// Build game states (this method handles its own locks)
	gameStates := s.buildGameStatesForAllPlayers(tableID)

	// Send notifications using lock-free methods
	if len(playerIDs) > 0 {
		for _, id := range playerIDs {
			s.notifyPlayer(id, reconnectNotification)
		}
	}

	// Send game state updates using lock-free method
	if len(gameStates) > 0 {
		s.sendGameStateUpdates(tableID, gameStates)
	}

	s.log.Infof("Player %s successfully reconnected to table %s", playerID, tableID)
	return nil
}

// handlePlayerDisconnection manages the state when a player disconnects
func (s *Server) handlePlayerDisconnection(tableID, playerID string) error {
	s.mu.Lock()

	table, ok := s.tables[tableID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("table not found")
	}

	// Check if player exists in table
	user := table.GetUser(playerID)
	if user == nil {
		s.mu.Unlock()
		return fmt.Errorf("player not found at table")
	}

	// Get player's chip balance if they're in a game
	var playerChips int64 = 0
	if table.IsGameStarted() && table.GetGame() != nil {
		game := table.GetGame()
		for _, player := range game.GetPlayers() {
			if player.ID == playerID {
				playerChips = player.Balance
				break
			}
		}
	}

	// If player has chips in an active game, mark as disconnected but keep in game
	if table.IsGameStarted() && playerChips > 0 {
		// Directly mark as disconnected to avoid attempting to re-enter the
		// server mutex inside markPlayerDisconnected() which would deadlock.
		user.IsDisconnected = true

		// Persist the updated disconnected flag.
		s.saveTableStateAsync(tableID, "player disconnected")

		// Collect all data needed for async operations while holding lock
		playerIDs := s.getTablePlayerIDs(tableID)
		gameStates := s.buildGameStatesForAllPlayers(tableID)

		disconnectNotification := &pokerrpc.Notification{
			Type:     pokerrpc.NotificationType_PLAYER_FOLDED, // Reuse folded notification type
			PlayerId: playerID,
			TableId:  tableID,
			Message:  fmt.Sprintf("Player %s has disconnected (chips preserved)", playerID),
		}

		s.mu.Unlock() // Release lock before async operations

		// Async operations using lock-free methods
		go func() {
			s.notifyPlayers(playerIDs, disconnectNotification)
			if gameStates != nil {
				s.sendGameStateUpdates(tableID, gameStates)
			}
		}()

		s.log.Infof("Player %s disconnected from table %s but kept in game (%d chips)", playerID, tableID, playerChips)
		return nil
	}

	// Player has no chips or game not started - can remove completely
	err := table.RemoveUser(playerID)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to remove user: %v", err)
	}

	// Delete player state from database
	err = s.db.DeletePlayerState(tableID, playerID)
	if err != nil {
		s.log.Errorf("Failed to delete player state from database: %v", err)
	}

	// Build notification data while holding lock
	leaveNotification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_PLAYER_FOLDED, // Reuse folded notification type
		PlayerId: playerID,
		TableId:  tableID,
		Message:  fmt.Sprintf("Player %s has left the table", playerID),
	}

	// Build game states for all players while holding the lock
	gameStates := s.buildGameStatesForAllPlayers(tableID)

	// Get player IDs while holding the lock
	playerIDs := s.getTablePlayerIDs(tableID)

	// Release lock before async operations
	s.mu.Unlock()

	// Simple async pattern for all notifications and state updates
	go func() {
		s.notifyPlayers(playerIDs, leaveNotification)
		// Send game state updates using lock-free approach
		if gameStates != nil {
			s.sendGameStateUpdates(tableID, gameStates)
		}
	}()

	// Re-acquire lock to ensure proper cleanup
	s.mu.Lock()

	s.log.Infof("Player %s disconnected and removed from table %s", playerID, tableID)
	return nil
}

// handlePlayerDisconnectionInternal manages the state when a player disconnects (called from notification queue)
func (s *Server) handlePlayerDisconnectionInternal(tableID, playerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	table, ok := s.tables[tableID]
	if !ok {
		return fmt.Errorf("table not found")
	}

	// Check if player exists in table
	user := table.GetUser(playerID)
	if user == nil {
		return fmt.Errorf("player not found at table")
	}

	// Get player's chip balance if they're in a game
	var playerChips int64 = 0
	if table.IsGameStarted() && table.GetGame() != nil {
		game := table.GetGame()
		for _, player := range game.GetPlayers() {
			if player.ID == playerID {
				playerChips = player.Balance
				break
			}
		}
	}

	// If player has chips in an active game, mark as disconnected but keep in game
	if table.IsGameStarted() && playerChips > 0 {
		// Directly mark as disconnected to avoid attempting to re-enter the
		// server mutex inside markPlayerDisconnected() which would deadlock.
		user.IsDisconnected = true

		// Collect all data needed for async operations while holding lock
		playerIDs := s.getTablePlayerIDs(tableID)
		gameStates := s.buildGameStatesForAllPlayers(tableID)

		// Build notification data while holding lock
		disconnectNotification := &pokerrpc.Notification{
			Type:     pokerrpc.NotificationType_PLAYER_FOLDED, // Reuse folded notification type
			PlayerId: playerID,
			TableId:  tableID,
			Message:  fmt.Sprintf("Player %s has disconnected (chips preserved)", playerID),
		}

		// Release lock before async operations
		s.mu.Unlock()

		// Simple async pattern for all notifications and state updates
		go func() {
			s.saveTableStateAsync(tableID, "player disconnected")
			s.notifyPlayers(playerIDs, disconnectNotification)
			// Send game state updates using lock-free approach
			if gameStates != nil {
				s.sendGameStateUpdates(tableID, gameStates)
			}
		}()

		// Re-acquire lock to ensure proper cleanup
		s.mu.Lock()

		s.log.Infof("Player %s disconnected from table %s but kept in game (%d chips)", playerID, tableID, playerChips)
		return nil
	}

	// Player has no chips or game not started - can remove completely
	err := table.RemoveUser(playerID)
	if err != nil {
		return fmt.Errorf("failed to remove user: %v", err)
	}

	// Delete player state from database
	err = s.db.DeletePlayerState(tableID, playerID)
	if err != nil {
		s.log.Errorf("Failed to delete player state from database: %v", err)
	}

	// Build game states for all players while holding the lock
	gameStates := s.buildGameStatesForAllPlayers(tableID)

	// Get player IDs while holding the lock
	playerIDs := s.getTablePlayerIDs(tableID)

	// Prepare notification while holding lock
	leaveNotification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_PLAYER_FOLDED, // Reuse folded notification type
		PlayerId: playerID,
		TableId:  tableID,
		Message:  fmt.Sprintf("Player %s has left the table", playerID),
	}

	// Save updated table state
	s.saveTableStateAsync(tableID, "player disconnected and removed")

	// Send notifications using lock-free approach (function is called with defer s.mu.Unlock())
	s.notifyPlayers(playerIDs, leaveNotification)

	// Send game state updates using lock-free approach
	if gameStates != nil {
		s.sendGameStateUpdates(tableID, gameStates)
	}

	s.log.Infof("Player %s disconnected and removed from table %s", playerID, tableID)
	return nil
}

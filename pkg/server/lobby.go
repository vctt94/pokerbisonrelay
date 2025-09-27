package server

import (
	"context"
	"fmt"
	"time"

	"github.com/vctt94/pokerbisonrelay/pkg/poker"
	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/pokerbisonrelay/pkg/server/internal/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) CreateTable(ctx context.Context, req *pokerrpc.CreateTableRequest) (*pokerrpc.CreateTableResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Get creator's DCR balance
	creatorBalance, err := s.db.GetPlayerBalance(req.PlayerId)
	if err != nil {
		return nil, err
	}

	s.log.Debugf("Creating table with buy-in %d", req.BuyIn)
	if creatorBalance < req.BuyIn {
		return nil, fmt.Errorf("insufficient DCR balance for buy-in: need %d, have %d", req.BuyIn, creatorBalance)
	}

	// Config
	timeBank := time.Duration(req.TimeBankSeconds) * time.Second
	if timeBank == 0 {
		timeBank = 30 * time.Second
	}
	startingChips := req.StartingChips
	if startingChips == 0 {
		startingChips = 1000
	}

	tblLog := s.logBackend.Logger("TABLE")
	gameLog := s.logBackend.Logger("GAME")

	cfg := poker.TableConfig{
		ID:             fmt.Sprintf("table_%d", time.Now().UnixNano()),
		Log:            tblLog,
		GameLog:        gameLog,
		HostID:         req.PlayerId,
		BuyIn:          req.BuyIn,
		MinPlayers:     int(req.MinPlayers),
		MaxPlayers:     int(req.MaxPlayers),
		SmallBlind:     req.SmallBlind,
		BigBlind:       req.BigBlind,
		MinBalance:     req.MinBalance,
		StartingChips:  startingChips,
		TimeBank:       timeBank,
		AutoStartDelay: time.Duration(req.AutoStartMs) * time.Millisecond,
	}

	// Create table
	table := poker.NewTable(cfg)
	table.SetStateSaver(s)

	// NEW: typed event pipeline adapter (poker -> server)
	table.SetEventPublisher(func(eventType string, tableID string, payload interface{}) {
		typ := GameEventType(eventType) // map poker's string to server enum
		s.log.Debugf("Publishing event to processor: %s for table %s", typ, tableID)

		ev, err := s.buildGameEvent(typ, tableID, payload)
		if err != nil {
			s.log.Errorf("failed to build %s event: %v", typ, err)
			return
		}
		s.eventProcessor.PublishEvent(ev)
	})

	// Seat creator
	if _, err := table.AddNewUser(req.PlayerId, req.PlayerId, creatorBalance, 0); err != nil {
		return nil, err
	}

	// Deduct buy-in
	if err := s.db.UpdatePlayerBalance(req.PlayerId, -req.BuyIn, "table buy-in", "created table"); err != nil {
		return nil, err
	}

	// Register table
	s.tables[cfg.ID] = table

	return &pokerrpc.CreateTableResponse{TableId: cfg.ID}, nil
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

func (s *Server) JoinTable(ctx context.Context, req *pokerrpc.JoinTableRequest) (*pokerrpc.JoinTableResponse, error) {
	s.mu.RLock()
	table, ok := s.tables[req.TableId]
	s.mu.RUnlock()
	if !ok {
		return &pokerrpc.JoinTableResponse{Success: false, Message: "Table not found"}, nil
	}

	s.log.Debugf("Joining table %s", req.TableId)

	config := table.GetConfig()

	// Reconnection path – player already seated.
	if existingUser := table.GetUser(req.PlayerId); existingUser != nil {
		// Publish typed PLAYER_JOINED event
		if evt, err := s.buildGameEvent(
			GameEventTypePlayerJoined,
			req.TableId,
			PlayerJoinedPayload{PlayerID: req.PlayerId},
		); err == nil {
			s.eventProcessor.PublishEvent(evt)
		} else {
			s.log.Errorf("Failed to build PLAYER_JOINED event: %v", err)
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

	// Publish typed PLAYER_JOINED event
	if evt, err := s.buildGameEvent(
		GameEventTypePlayerJoined,
		req.TableId,
		PlayerJoinedPayload{PlayerID: req.PlayerId},
	); err == nil {
		s.eventProcessor.PublishEvent(evt)
	} else {
		s.log.Errorf("Failed to build PLAYER_JOINED event: %v", err)
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

	// Publish typed PLAYER_READY event
	if event, err := s.buildGameEvent(
		GameEventTypePlayerReady,
		req.TableId,
		PlayerReadyPayload{PlayerID: req.PlayerId},
	); err == nil {
		s.eventProcessor.PublishEvent(event)
	} else {
		s.log.Errorf("Failed to build PLAYER_READY event: %v", err)
	}

	// If all players are ready and the game hasn't started yet, start the game
	if allReady && !gameStarted {
		if errStart := table.StartGame(); errStart != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to start game: %v", errStart))
		}

		// Publish typed GAME_STARTED event *after* the game has been
		// successfully created so that the emitted snapshot reflects the brand-new
		// game state (dealer, blinds, current player, etc.). Without this, the first
		// game update received by the clients would still be in the pre-start state
		// which prevents the UI from progressing to the actual hand.
		if gameStartedEvent, errGS := s.buildGameEvent(
			GameEventTypeGameStarted,
			req.TableId,
			GameStartedPayload{PlayerIDs: []string{req.PlayerId}},
		); errGS == nil {
			s.eventProcessor.PublishEvent(gameStartedEvent)
		} else {
			s.log.Errorf("Failed to build GAME_STARTED event: %v", errGS)
		}

		// Attach callback to broadcast NEW_HAND_STARTED events triggered by auto-start logic
		if g := table.GetGame(); g != nil {
			g.SetOnNewHandStartedCallback(func() {
				// Publish typed NEW_HAND_STARTED event
				if evt, err := s.buildGameEvent(
					GameEventTypeNewHandStarted,
					req.TableId,
					NewHandStartedPayload{},
				); err == nil {
					s.eventProcessor.PublishEvent(evt)
				} else {
					s.log.Errorf("Failed to build NEW_HAND_STARTED event: %v", err)
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

	// Publish typed PLAYER_READY event (with ready=false)
	if event, err := s.buildGameEvent(
		GameEventTypePlayerReady,
		req.TableId,
		PlayerMarkedReadyPayload{PlayerID: req.PlayerId, Ready: false},
	); err == nil {
		s.eventProcessor.PublishEvent(event)
	} else {
		s.log.Errorf("Failed to build PLAYER_READY event: %v", err)
	}

	return &pokerrpc.SetPlayerUnreadyResponse{
		Success: true,
		Message: "Player is unready",
	}, nil
}

// StartNotificationStream handles notification streaming
func (s *Server) StartNotificationStream(req *pokerrpc.StartNotificationStreamRequest, stream pokerrpc.LobbyService_StartNotificationStreamServer) error {
	playerID := req.PlayerId
	if playerID == "" {
		return status.Error(codes.InvalidArgument, "player ID is required")
	}

	// Create notification stream
	notifStream := &NotificationStream{
		playerID: playerID,
		stream:   stream,
		done:     make(chan struct{}),
	}

	// Register the stream
	s.notificationMu.Lock()
	s.notificationStreams[playerID] = notifStream
	s.notificationMu.Unlock()

	// Remove stream when done
	defer func() {
		s.notificationMu.Lock()
		delete(s.notificationStreams, playerID)
		s.notificationMu.Unlock()
		close(notifStream.done)
	}()

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
	ctx := stream.Context()
	<-ctx.Done()
	return nil
}

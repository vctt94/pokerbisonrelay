package server

import (
	"fmt"

	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

// broadcastNotification sends a notification to a specific player
func (s *Server) sendNotificationToPlayer(playerID string, notification *pokerrpc.Notification) {
	s.notificationMu.RLock()
	notifStream, exists := s.notificationStreams[playerID]
	s.notificationMu.RUnlock()

	if !exists {
		return // Player doesn't have an active notification stream
	}

	select {
	case <-notifStream.done:
		return // Stream is closed
	default:
		// Send notification, ignore errors as client might have disconnected
		notifStream.stream.Send(notification)
	}
}

// broadcastNotificationToTable sends a notification to all players at a table
func (s *Server) broadcastNotificationToTable(tableID string, notification *pokerrpc.Notification) {
	s.mu.RLock()
	table, exists := s.tables[tableID]
	s.mu.RUnlock()

	if !exists {
		return
	}

	users := table.GetUsers()
	for _, user := range users {
		s.sendNotificationToPlayer(user.ID, notification)
	}
}

// notifyPlayers sends a notification to specific players
// This version doesn't acquire the server mutex, requiring player IDs to be passed as parameters
func (s *Server) notifyPlayers(playerIDs []string, notification *pokerrpc.Notification) {
	for _, playerID := range playerIDs {
		s.notifyPlayer(playerID, notification)
	}
}

// NotificationSender interface implementation

// SendAllPlayersReady sends ALL_PLAYERS_READY notification to all players at the table
func (s *Server) SendAllPlayersReady(tableID string) {
	notification := &pokerrpc.Notification{
		Type:    pokerrpc.NotificationType_ALL_PLAYERS_READY,
		Message: "All players are ready! Game starting soon...",
		TableId: tableID,
	}

	go func() {
		s.broadcastNotificationToTable(tableID, notification)
	}()
}

// SendGameStarted sends GAME_STARTED notification to all players at the table
func (s *Server) SendGameStarted(tableID string) {
	notification := &pokerrpc.Notification{
		Type:    pokerrpc.NotificationType_GAME_STARTED,
		Message: "Game started!",
		TableId: tableID,
		Started: true,
	}

	go func() {
		s.broadcastNotificationToTable(tableID, notification)
	}()
}

// SendNewHandStarted sends NEW_HAND_STARTED notification to all players at the table
func (s *Server) SendNewHandStarted(tableID string) {
	notification := &pokerrpc.Notification{
		Type:    pokerrpc.NotificationType_NEW_HAND_STARTED,
		Message: "New hand started!",
		TableId: tableID,
	}

	go func() {
		s.broadcastNotificationToTable(tableID, notification)
	}()
}

// SendPlayerReady sends PLAYER_READY notification to all players at the table
func (s *Server) SendPlayerReady(tableID, playerID string, ready bool) {
	var notificationType pokerrpc.NotificationType
	var message string

	if ready {
		notificationType = pokerrpc.NotificationType_PLAYER_READY
		message = fmt.Sprintf("%s is now ready", playerID)
	} else {
		notificationType = pokerrpc.NotificationType_PLAYER_UNREADY
		message = fmt.Sprintf("%s is no longer ready", playerID)
	}

	notification := &pokerrpc.Notification{
		Type:     notificationType,
		Message:  message,
		TableId:  tableID,
		PlayerId: playerID,
		Ready:    ready,
	}

	go func() {
		s.broadcastNotificationToTable(tableID, notification)
	}()
}

// SendBlindPosted sends blind posted notification to all players at the table
func (s *Server) SendBlindPosted(tableID, playerID string, amount int64, isSmallBlind bool) {
	var notificationType pokerrpc.NotificationType
	var message string

	if isSmallBlind {
		notificationType = pokerrpc.NotificationType_SMALL_BLIND_POSTED
		message = fmt.Sprintf("Small blind posted: %d chips", amount)
	} else {
		notificationType = pokerrpc.NotificationType_BIG_BLIND_POSTED
		message = fmt.Sprintf("Big blind posted: %d chips", amount)
	}

	notification := &pokerrpc.Notification{
		Type:     notificationType,
		Message:  message,
		TableId:  tableID,
		PlayerId: playerID,
		Amount:   amount,
	}

	go func() {
		s.broadcastNotificationToTable(tableID, notification)
	}()
}

// SendShowdownResult sends SHOWDOWN_RESULT notification to all players at the table
func (s *Server) SendShowdownResult(tableID string, winners []*pokerrpc.Winner, pot int64) {
	notification := &pokerrpc.Notification{
		Type:    pokerrpc.NotificationType_SHOWDOWN_RESULT,
		Message: fmt.Sprintf("Showdown complete! Pot: %d chips", pot),
		TableId: tableID,
		Winners: winners,
		Amount:  pot,
	}

	go func() {
		s.broadcastNotificationToTable(tableID, notification)
	}()
}

// notifyPlayer sends a notification to a specific player
// This version only uses the notification mutex, not the main server mutex
func (s *Server) notifyPlayer(playerID string, notification *pokerrpc.Notification) {
	s.notificationMu.RLock()
	notifStream, exists := s.notificationStreams[playerID]
	s.notificationMu.RUnlock()

	if !exists {
		return // Player doesn't have an active notification stream
	}

	select {
	case <-notifStream.done:
		return // Stream is closed
	default:
		// Send notification, ignore errors as client might have disconnected
		notifStream.stream.Send(notification)
	}
}

// sendGameStateUpdates sends pre-built game states to players
// This version only uses the game streams mutex, not the main server mutex
func (s *Server) sendGameStateUpdates(tableID string, playerGameStates map[string]*pokerrpc.GameUpdate) {
	s.gameStreamsMu.RLock()
	playerStreams, exists := s.gameStreams[tableID]
	s.gameStreamsMu.RUnlock()

	if !exists || len(playerStreams) == 0 {
		return
	}

	s.log.Debugf("sendGameStateUpdates: broadcasting to %d players on table %s", len(playerStreams), tableID)

	// Send pre-built game states to each player stream
	// Use a single goroutine to avoid goroutine explosion
	go func() {
		for playerID, stream := range playerStreams {
			if gameState, ok := playerGameStates[playerID]; ok {
				// Send the update, ignore errors as client might have disconnected
				stream.Send(gameState)
			}
		}
	}()
}

// tablePlayerIDs returns the list of player IDs currently seated at the given
// table. A short-lived read lock protects the map lookup; the table itself is
// already thread-safe.
func (s *Server) tablePlayerIDs(tableID string) []string {
	s.mu.RLock()
	tbl, ok := s.tables[tableID]
	s.mu.RUnlock()
	if !ok {
		return nil
	}

	users := tbl.GetUsers()
	ids := make([]string, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	return ids
}

// getTablePlayerIDs is kept for backward-compatibility with existing callers.
// It simply delegates to tablePlayerIDs.
func (s *Server) getTablePlayerIDs(tableID string) []string { return s.tablePlayerIDs(tableID) }

// Helper method to build game states for all players while holding lock
func (s *Server) buildGameStatesForAllPlayers(tableID string) map[string]*pokerrpc.GameUpdate {
	// Get game stream player IDs (players who need game state updates)
	s.gameStreamsMu.RLock()
	playerStreams, exists := s.gameStreams[tableID]
	s.gameStreamsMu.RUnlock()

	if !exists || len(playerStreams) == 0 {
		return nil
	}

	// Build game states for all players at once to minimize lock contention
	gameStates := make(map[string]*pokerrpc.GameUpdate)
	for playerID := range playerStreams {
		// Use buildGameState which handles its own locking
		gameState, err := s.buildGameState(tableID, playerID)
		if err != nil {
			s.log.Debugf("Failed to build game state for player %s: %v", playerID, err)
			continue
		}
		gameStates[playerID] = gameState
	}

	return gameStates
}

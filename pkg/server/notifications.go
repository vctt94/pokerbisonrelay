package server

import (
	"fmt"

	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
)

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

// broadcastNotificationToAll sends a notification to all connected players
// that currently have an active notification stream.
func (s *Server) broadcastNotificationToAll(notification *pokerrpc.Notification) {
	s.notificationMu.RLock()
	for _, notifStream := range s.notificationStreams {
		select {
		case <-notifStream.done:
			// Skip closed streams
			continue
		default:
			// Best-effort send; ignore errors if client disconnected
			_ = notifStream.stream.Send(notification)
		}
	}
	s.notificationMu.RUnlock()
}

// broadcastNotificationToTable sends a notification to all players at a table
func (s *Server) broadcastNotificationToTable(tableID string, notification *pokerrpc.Notification) {
	table, exists := s.getTable(tableID)

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

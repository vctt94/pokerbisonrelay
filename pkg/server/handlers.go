package server

import (
	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
)

// EventHandler defines the interface for handling events
type EventHandler interface {
	HandleEvent(event *GameEvent)
}

// NotificationHandler handles broadcasting notifications for events
type NotificationHandler struct {
	server *Server
}

// NewNotificationHandler creates a new notification handler
func NewNotificationHandler(server *Server) *NotificationHandler {
	return &NotificationHandler{server: server}
}

// HandleEvent processes an event and broadcasts appropriate notifications
func (nh *NotificationHandler) HandleEvent(event *GameEvent) {
	switch event.Type {
	case GameEventTypeBetMade:
		nh.handleBetMade(event)
	case GameEventTypePlayerFolded:
		nh.handlePlayerFolded(event)
	case GameEventTypeCallMade:
		nh.handleCallMade(event)
	case GameEventTypeCheckMade:
		nh.handleCheckMade(event)
	case GameEventTypeGameStarted:
		nh.handleGameStarted(event)
	case GameEventTypeGameEnded:
		nh.handleGameEnded(event)
	case GameEventTypePlayerReady:
		nh.handlePlayerReady(event)
	case GameEventTypePlayerJoined:
		nh.handlePlayerJoined(event)
	case GameEventTypePlayerLeft:
		nh.handlePlayerLeft(event)
	case GameEventTypeNewHandStarted:
		nh.handleNewHandStarted(event)
	}
}

func (nh *NotificationHandler) handleBetMade(event *GameEvent) {
	notification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_BET_MADE,
		PlayerId: event.Metadata["playerID"].(string),
		TableId:  event.TableID,
		Amount:   event.Amount,
		Message:  event.Metadata["message"].(string),
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handlePlayerFolded(event *GameEvent) {
	notification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_PLAYER_FOLDED,
		PlayerId: event.Metadata["playerID"].(string),
		TableId:  event.TableID,
		Message:  event.Metadata["message"].(string),
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handleCallMade(event *GameEvent) {
	notification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_CALL_MADE,
		PlayerId: event.Metadata["playerID"].(string),
		TableId:  event.TableID,
		Amount:   event.Amount,
		Message:  event.Metadata["message"].(string),
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handleCheckMade(event *GameEvent) {
	notification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_CHECK_MADE,
		PlayerId: event.Metadata["playerID"].(string),
		TableId:  event.TableID,
		Message:  event.Metadata["message"].(string),
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handleGameStarted(event *GameEvent) {
	notification := &pokerrpc.Notification{
		Type:    pokerrpc.NotificationType_GAME_STARTED,
		TableId: event.TableID,
		Message: event.Metadata["message"].(string),
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handleGameEnded(event *GameEvent) {
	notification := &pokerrpc.Notification{
		Type:    pokerrpc.NotificationType_GAME_ENDED,
		TableId: event.TableID,
		Message: event.Metadata["message"].(string),
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handlePlayerReady(event *GameEvent) {
	notification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_PLAYER_READY,
		PlayerId: event.Metadata["playerID"].(string),
		TableId:  event.TableID,
		Message:  event.Metadata["message"].(string),
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handlePlayerJoined(event *GameEvent) {
	notification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_PLAYER_JOINED,
		PlayerId: event.Metadata["playerID"].(string),
		TableId:  event.TableID,
		Message:  event.Metadata["message"].(string),
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handlePlayerLeft(event *GameEvent) {
	notification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_PLAYER_LEFT,
		PlayerId: event.Metadata["playerID"].(string),
		TableId:  event.TableID,
		Message:  event.Metadata["message"].(string),
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handleNewHandStarted(event *GameEvent) {
	notification := &pokerrpc.Notification{
		Type:    pokerrpc.NotificationType_NEW_HAND_STARTED,
		TableId: event.TableID,
		Message: event.Metadata["message"].(string),
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

// GameStateHandler handles broadcasting game state updates for events
type GameStateHandler struct {
	server *Server
}

// NewGameStateHandler creates a new game state handler
func NewGameStateHandler(server *Server) *GameStateHandler {
	return &GameStateHandler{server: server}
}

// HandleEvent processes an event and broadcasts game state updates
func (gsh *GameStateHandler) HandleEvent(event *GameEvent) {
	// Build game states from the event snapshot
	gameStates := gsh.buildGameStatesFromSnapshot(event.TableSnapshot)
	if len(gameStates) > 0 {
		gsh.server.sendGameStateUpdates(event.TableID, gameStates)
	}
}

// buildGameStatesFromSnapshot creates game states for all players from a table snapshot
func (gsh *GameStateHandler) buildGameStatesFromSnapshot(snapshot *TableSnapshot) map[string]*pokerrpc.GameUpdate {
	if snapshot == nil {
		return nil
	}

	gameStates := make(map[string]*pokerrpc.GameUpdate)

	for _, playerSnapshot := range snapshot.Players {
		gameUpdate := gsh.buildGameUpdateFromSnapshot(snapshot, playerSnapshot.ID)
		if gameUpdate != nil {
			gameStates[playerSnapshot.ID] = gameUpdate
		}
	}

	return gameStates
}

// buildGameUpdateFromSnapshot creates a GameUpdate for a specific player from snapshots
func (gsh *GameStateHandler) buildGameUpdateFromSnapshot(tableSnapshot *TableSnapshot, requestingPlayerID string) *pokerrpc.GameUpdate {
	// Build players list from snapshot data
	var players []*pokerrpc.Player
	for _, playerSnapshot := range tableSnapshot.Players {
		player := &pokerrpc.Player{
			Id:         playerSnapshot.ID,
			Balance:    playerSnapshot.Balance,
			IsReady:    playerSnapshot.IsReady,
			Folded:     playerSnapshot.HasFolded,
			CurrentBet: playerSnapshot.HasBet,
		}

		// Show cards if it's the requesting player's own data or during showdown
		if playerSnapshot.ID == requestingPlayerID ||
			(tableSnapshot.GameSnapshot != nil && tableSnapshot.GameSnapshot.Phase == pokerrpc.GamePhase_SHOWDOWN) {
			player.Hand = make([]*pokerrpc.Card, len(playerSnapshot.Hand))
			for i, card := range playerSnapshot.Hand {
				player.Hand[i] = &pokerrpc.Card{
					Suit:  card.GetSuit(),
					Value: card.GetValue(),
				}
			}
		}

		// Include hand description during showdown
		if tableSnapshot.GameSnapshot != nil && tableSnapshot.GameSnapshot.Phase == pokerrpc.GamePhase_SHOWDOWN {
			player.HandDescription = playerSnapshot.HandDescription
		}

		players = append(players, player)
	}

	// Build community cards slice
	communityCards := make([]*pokerrpc.Card, 0)
	var pot int64 = 0
	var currentBet int64 = 0
	var gamePhase pokerrpc.GamePhase = pokerrpc.GamePhase_WAITING
	var currentPlayerID string

	if tableSnapshot.GameSnapshot != nil {
		pot = tableSnapshot.GameSnapshot.Pot
		currentBet = tableSnapshot.GameSnapshot.CurrentBet
		gamePhase = tableSnapshot.GameSnapshot.Phase
		currentPlayerID = tableSnapshot.GameSnapshot.CurrentPlayer

		for _, card := range tableSnapshot.GameSnapshot.CommunityCards {
			communityCards = append(communityCards, &pokerrpc.Card{
				Suit:  card.GetSuit(),
				Value: card.GetValue(),
			})
		}
	}

	return &pokerrpc.GameUpdate{
		TableId:         tableSnapshot.ID,
		Phase:           gamePhase,
		PhaseName:       gamePhase.String(),
		Players:         players,
		CommunityCards:  communityCards,
		Pot:             pot,
		CurrentBet:      currentBet,
		CurrentPlayer:   currentPlayerID,
		GameStarted:     tableSnapshot.State.GameStarted,
		PlayersRequired: int32(tableSnapshot.Config.MinPlayers),
		PlayersJoined:   int32(tableSnapshot.State.PlayerCount),
	}
}

// PersistenceHandler handles state persistence for events
type PersistenceHandler struct {
	server *Server
}

// NewPersistenceHandler creates a new persistence handler
func NewPersistenceHandler(server *Server) *PersistenceHandler {
	return &PersistenceHandler{server: server}
}

// HandleEvent processes an event and persists state changes
func (ph *PersistenceHandler) HandleEvent(event *GameEvent) {
	// Save table state asynchronously using existing method
	ph.server.saveTableStateAsync(event.TableID, string(event.Type))
}

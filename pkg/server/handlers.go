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
	case GameEventTypeShowdownResult:
		nh.handleShowdownResult(event)
	}
}

func (nh *NotificationHandler) handleBetMade(event *GameEvent) {
	pl, ok := event.Payload.(BetMadePayload)
	if !ok {
		nh.server.log.Warnf("BET_MADE without BetMadePayload; skipping (table=%s)", event.TableID)
		return
	}
	notification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_BET_MADE,
		PlayerId: pl.PlayerID,
		TableId:  event.TableID,
		Amount:   pl.Amount,
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handlePlayerFolded(event *GameEvent) {
	pl, ok := event.Payload.(PlayerFoldedPayload)
	if !ok {
		nh.server.log.Warnf("PLAYER_FOLDED without PlayerFoldedPayload; skipping (table=%s)", event.TableID)
		return
	}
	notification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_PLAYER_FOLDED,
		PlayerId: pl.PlayerID,
		TableId:  event.TableID,
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handleCallMade(event *GameEvent) {
	pl, ok := event.Payload.(CallMadePayload)
	if !ok {
		nh.server.log.Warnf("CALL_MADE without CallMadePayload; skipping (table=%s)", event.TableID)
		return
	}
	notification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_CALL_MADE,
		PlayerId: pl.PlayerID,
		TableId:  event.TableID,
		Amount:   pl.Amount, // e.g., amount called; adjust field name if different
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handleCheckMade(event *GameEvent) {
	pl, ok := event.Payload.(CheckMadePayload)
	if !ok {
		nh.server.log.Warnf("CHECK_MADE without CheckMadePayload; skipping (table=%s)", event.TableID)
		return
	}
	notification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_CHECK_MADE,
		PlayerId: pl.PlayerID,
		TableId:  event.TableID,
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handleGameStarted(event *GameEvent) {
	// payload optional; we only need table id here
	notification := &pokerrpc.Notification{
		Type:    pokerrpc.NotificationType_GAME_STARTED,
		TableId: event.TableID,
		Started: true,
	}
	nh.server.log.Debugf("Sending GAME_STARTED notification to %d players: %v", len(event.PlayerIDs), event.PlayerIDs)
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handleGameEnded(event *GameEvent) {
	// If you have a typed payload (e.g., winner summary), assert it here
	notification := &pokerrpc.Notification{
		Type:    pokerrpc.NotificationType_GAME_ENDED,
		TableId: event.TableID,
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handlePlayerReady(event *GameEvent) {
	pl, ok := event.Payload.(PlayerReadyPayload)
	if !ok {
		nh.server.log.Warnf("PLAYER_READY without PlayerReadyPayload; skipping (table=%s)", event.TableID)
		return
	}
	notification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_PLAYER_READY,
		PlayerId: pl.PlayerID,
		TableId:  event.TableID,
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handlePlayerJoined(event *GameEvent) {
	pl, ok := event.Payload.(PlayerJoinedPayload)
	if !ok {
		nh.server.log.Warnf("PLAYER_JOINED without PlayerJoinedPayload; skipping (table=%s)", event.TableID)
		return
	}
	notification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_PLAYER_JOINED,
		PlayerId: pl.PlayerID,
		TableId:  event.TableID,
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handlePlayerLeft(event *GameEvent) {
	pl, ok := event.Payload.(PlayerLeftPayload)
	if !ok {
		nh.server.log.Warnf("PLAYER_LEFT without PlayerLeftPayload; skipping (table=%s)", event.TableID)
		return
	}
	notification := &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_PLAYER_LEFT,
		PlayerId: pl.PlayerID,
		TableId:  event.TableID,
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handleNewHandStarted(event *GameEvent) {
	// If you define a payload (e.g., handID, dealerPos), assert/use it here
	notification := &pokerrpc.Notification{
		Type:    pokerrpc.NotificationType_NEW_HAND_STARTED,
		TableId: event.TableID,
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

func (nh *NotificationHandler) handleShowdownResult(event *GameEvent) {
	sp, ok := event.Payload.(ShowdownPayload)
	if !ok {
		nh.server.log.Warnf("SHOWDOWN_RESULT without ShowdownPayload; skipping (table=%s)", event.TableID)
		return
	}
	notification := &pokerrpc.Notification{
		Type:    pokerrpc.NotificationType_SHOWDOWN_RESULT,
		TableId: event.TableID,
		Showdown: &pokerrpc.Showdown{
			Winners: sp.Winners,
			Pot:     sp.Pot,
		},
	}
	nh.server.notifyPlayers(event.PlayerIDs, notification)
}

// ------------------------ Game State Handler ------------------------

type GameStateHandler struct {
	server *Server
}

func NewGameStateHandler(server *Server) *GameStateHandler {
	return &GameStateHandler{server: server}
}

func (gsh *GameStateHandler) HandleEvent(event *GameEvent) {
	// Build game states from the event snapshot
	gameStates := gsh.buildGameStatesFromSnapshot(event.TableSnapshot)
	if len(gameStates) > 0 {
		gsh.server.sendGameStateUpdates(event.TableID, gameStates)
	}
}

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

func (gsh *GameStateHandler) buildGameUpdateFromSnapshot(tableSnapshot *TableSnapshot, requestingPlayerID string) *pokerrpc.GameUpdate {
	if tableSnapshot == nil {
		return nil
	}

	// Build players list from snapshot data
	var players []*pokerrpc.Player
	for _, ps := range tableSnapshot.Players {
		player := &pokerrpc.Player{
			Id:         ps.ID,
			Balance:    ps.Balance,
			IsReady:    ps.IsReady,
			Folded:     ps.HasFolded,
			CurrentBet: ps.HasBet,
		}

		// Show cards if it's the requesting player's own data or during showdown
		if ps.ID == requestingPlayerID ||
			(tableSnapshot.GameSnapshot != nil && tableSnapshot.GameSnapshot.Phase == pokerrpc.GamePhase_SHOWDOWN) {
			player.Hand = make([]*pokerrpc.Card, len(ps.Hand))
			for i, card := range ps.Hand {
				player.Hand[i] = &pokerrpc.Card{
					Suit:  card.GetSuit(),
					Value: card.GetValue(),
				}
			}
		}

		// Include hand description during showdown
		if tableSnapshot.GameSnapshot != nil && tableSnapshot.GameSnapshot.Phase == pokerrpc.GamePhase_SHOWDOWN {
			player.HandDescription = ps.HandDescription
		}

		players = append(players, player)
	}

	// Build community cards slice
	var communityCards []*pokerrpc.Card
	var pot int64
	var currentBet int64
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

// ------------------------ Persistence Handler ------------------------

type PersistenceHandler struct {
	server *Server
}

func NewPersistenceHandler(server *Server) *PersistenceHandler {
	return &PersistenceHandler{server: server}
}

func (ph *PersistenceHandler) HandleEvent(event *GameEvent) {
	// Save table state asynchronously using existing method
	ph.server.saveTableStateAsync(event.TableID, string(event.Type))
}

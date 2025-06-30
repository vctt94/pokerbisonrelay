package server

import (
	"fmt"
	"time"

	"github.com/vctt94/poker-bisonrelay/pkg/poker"
)

// collectTableSnapshot collects a complete immutable snapshot of table state
func (s *Server) collectTableSnapshot(tableID string) (*TableSnapshot, error) {
	// Use a read lock only while accessing the tables map. This avoids holding
	// the server-wide lock for the entire snapshot generation while still
	// guaranteeing a consistent pointer to the table.
	s.mu.RLock()
	table, ok := s.tables[tableID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("table not found: %s", tableID)
	}

	config := table.GetConfig()
	users := table.GetUsers()
	game := table.GetGame()

	// Collect player snapshots
	playerSnapshots := make([]*PlayerSnapshot, 0, len(users))
	for _, user := range users {
		snapshot := s.collectPlayerSnapshot(user, game)
		playerSnapshots = append(playerSnapshots, snapshot)
	}

	// Collect game snapshot if game exists
	var gameSnapshot *GameSnapshot
	if game != nil {
		gameSnapshot = s.collectGameSnapshot(game)
	}

	// Collect table state
	tableState := TableState{
		GameStarted:     table.IsGameStarted(),
		AllPlayersReady: table.AreAllPlayersReady(),
		PlayerCount:     len(users),
	}

	return &TableSnapshot{
		ID:           tableID,
		Players:      playerSnapshots,
		GameSnapshot: gameSnapshot,
		Config:       config,
		State:        tableState,
		Timestamp:    time.Now(),
	}, nil
}

// collectPlayerSnapshot collects an immutable snapshot of player state
func (s *Server) collectPlayerSnapshot(user *poker.User, game *poker.Game) *PlayerSnapshot {
	snapshot := &PlayerSnapshot{
		ID:                user.ID,
		TableSeat:         user.TableSeat,
		Balance:           0, // Default for users not in game
		Hand:              make([]poker.Card, 0),
		DCRAccountBalance: user.DCRAccountBalance,
		IsReady:           user.IsReady,
		IsDisconnected:    false,
		HasFolded:         false,
		IsAllIn:           false,
		IsDealer:          false,
		IsTurn:            false,
		GameState:         "AT_TABLE",
		HandDescription:   "",
		HasBet:            0,
		StartingBalance:   0,
	}

	// If game exists and player is in it, get game-specific data
	if game != nil {
		for _, player := range game.GetPlayers() {
			if player.ID == user.ID {
				snapshot.Balance = player.Balance
				snapshot.HasFolded = player.HasFolded
				snapshot.IsAllIn = player.IsAllIn
				snapshot.IsDealer = player.IsDealer
				snapshot.IsTurn = player.IsTurn
				snapshot.GameState = player.GetGameState()
				snapshot.HandDescription = player.HandDescription
				snapshot.HasBet = player.HasBet
				snapshot.StartingBalance = player.StartingBalance

				// Deep copy hand cards to ensure immutability
				if len(player.Hand) > 0 {
					snapshot.Hand = make([]poker.Card, len(player.Hand))
					copy(snapshot.Hand, player.Hand)
				}
				break
			}
		}
	}

	return snapshot
}

// collectGameSnapshot collects an immutable snapshot of game state
func (s *Server) collectGameSnapshot(game *poker.Game) *GameSnapshot {
	snapshot := &GameSnapshot{
		Phase:      game.GetPhase(),
		Pot:        game.GetPot(),
		CurrentBet: game.GetCurrentBet(),
		Dealer:     game.GetDealer(),
		Round:      game.GetRound(),
		BetRound:   game.GetBetRound(),
		Winners:    make([]string, 0),
	}

	// Get current player
	if currentPlayerObj := game.GetCurrentPlayerObject(); currentPlayerObj != nil {
		snapshot.CurrentPlayer = currentPlayerObj.ID
	}

	// Deep copy community cards to ensure immutability
	communityCards := game.GetCommunityCards()
	if len(communityCards) > 0 {
		snapshot.CommunityCards = make([]poker.Card, len(communityCards))
		copy(snapshot.CommunityCards, communityCards)
	}

	// Get winners if available
	winners := game.GetWinners()
	if len(winners) > 0 {
		snapshot.Winners = make([]string, len(winners))
		copy(snapshot.Winners, winners)
	}

	return snapshot
}

// buildGameEvent constructs a GameEvent with a fresh table snapshot and the
// list of current player IDs. It centralises the repetitive logic that was
// previously duplicated across the individual collectors.
func (s *Server) buildGameEvent(eventType GameEventType, tableID string, amount int64, metadata map[string]interface{}) (*GameEvent, error) {
	tableSnapshot, err := s.collectTableSnapshot(tableID)
	if err != nil {
		return nil, err
	}

	playerIDs := s.getTablePlayerIDsCol(tableID)

	return &GameEvent{
		Type:          eventType,
		TableID:       tableID,
		PlayerIDs:     playerIDs,
		Amount:        amount,
		Metadata:      metadata,
		Timestamp:     time.Now(),
		TableSnapshot: tableSnapshot,
	}, nil
}

// BetMadeCollector handles snapshot collection for bet made events
type BetMadeCollector struct{}

func (c *BetMadeCollector) EventType() GameEventType {
	return GameEventTypeBetMade
}

func (c *BetMadeCollector) CollectSnapshot(s *Server, tableID, playerID string, amount int64, metadata map[string]interface{}) (*GameEvent, error) {
	return s.buildGameEvent(GameEventTypeBetMade, tableID, amount, metadata)
}

// PlayerFoldedCollector handles snapshot collection for player folded events
type PlayerFoldedCollector struct{}

func (c *PlayerFoldedCollector) EventType() GameEventType {
	return GameEventTypePlayerFolded
}

func (c *PlayerFoldedCollector) CollectSnapshot(s *Server, tableID, playerID string, amount int64, metadata map[string]interface{}) (*GameEvent, error) {
	return s.buildGameEvent(GameEventTypePlayerFolded, tableID, amount, metadata)
}

// CallMadeCollector handles snapshot collection for call made events
type CallMadeCollector struct{}

func (c *CallMadeCollector) EventType() GameEventType {
	return GameEventTypeCallMade
}

func (c *CallMadeCollector) CollectSnapshot(s *Server, tableID, playerID string, amount int64, metadata map[string]interface{}) (*GameEvent, error) {
	return s.buildGameEvent(GameEventTypeCallMade, tableID, amount, metadata)
}

// CheckMadeCollector handles snapshot collection for check made events
type CheckMadeCollector struct{}

func (c *CheckMadeCollector) EventType() GameEventType {
	return GameEventTypeCheckMade
}

func (c *CheckMadeCollector) CollectSnapshot(s *Server, tableID, playerID string, amount int64, metadata map[string]interface{}) (*GameEvent, error) {
	return s.buildGameEvent(GameEventTypeCheckMade, tableID, amount, metadata)
}

// GameStartedCollector handles snapshot collection for game started events
type GameStartedCollector struct{}

func (c *GameStartedCollector) EventType() GameEventType {
	return GameEventTypeGameStarted
}

func (c *GameStartedCollector) CollectSnapshot(s *Server, tableID, playerID string, amount int64, metadata map[string]interface{}) (*GameEvent, error) {
	return s.buildGameEvent(GameEventTypeGameStarted, tableID, amount, metadata)
}

// GameEndedCollector handles snapshot collection for game ended events
type GameEndedCollector struct{}

func (c *GameEndedCollector) EventType() GameEventType {
	return GameEventTypeGameEnded
}

func (c *GameEndedCollector) CollectSnapshot(s *Server, tableID, playerID string, amount int64, metadata map[string]interface{}) (*GameEvent, error) {
	return s.buildGameEvent(GameEventTypeGameEnded, tableID, amount, metadata)
}

// PlayerReadyCollector handles snapshot collection for player ready events
type PlayerReadyCollector struct{}

func (c *PlayerReadyCollector) EventType() GameEventType {
	return GameEventTypePlayerReady
}

func (c *PlayerReadyCollector) CollectSnapshot(s *Server, tableID, playerID string, amount int64, metadata map[string]interface{}) (*GameEvent, error) {
	return s.buildGameEvent(GameEventTypePlayerReady, tableID, amount, metadata)
}

// PlayerJoinedCollector handles snapshot collection for player joined events
type PlayerJoinedCollector struct{}

func (c *PlayerJoinedCollector) EventType() GameEventType {
	return GameEventTypePlayerJoined
}

func (c *PlayerJoinedCollector) CollectSnapshot(s *Server, tableID, playerID string, amount int64, metadata map[string]interface{}) (*GameEvent, error) {
	// Attempt to collect a table snapshot. If the table doesn't exist yet (e.g. tests
	// creating an event before the table is added to the server), continue without
	// failing – a nil TableSnapshot is acceptable for join notifications.
	tableSnapshot, _ := s.collectTableSnapshot(tableID)

	// Determine which players should receive this event. If the table exists, use all
	// seated player IDs; otherwise default to broadcasting only to the joining
	// player so the metadata injection logic in tests still works.
	var playerIDs []string
	s.mu.RLock()
	_, exists := s.tables[tableID]
	s.mu.RUnlock()
	if exists {
		playerIDs = s.getTablePlayerIDsCol(tableID)
	} else {
		playerIDs = []string{playerID}
	}

	return &GameEvent{
		Type:          GameEventTypePlayerJoined,
		TableID:       tableID,
		PlayerIDs:     playerIDs,
		Amount:        amount,
		Metadata:      metadata,
		Timestamp:     time.Now(),
		TableSnapshot: tableSnapshot,
	}, nil
}

// PlayerLeftCollector handles snapshot collection for player left events
type PlayerLeftCollector struct{}

func (c *PlayerLeftCollector) EventType() GameEventType {
	return GameEventTypePlayerLeft
}

func (c *PlayerLeftCollector) CollectSnapshot(s *Server, tableID, playerID string, amount int64, metadata map[string]interface{}) (*GameEvent, error) {
	return s.buildGameEvent(GameEventTypePlayerLeft, tableID, amount, metadata)
}

// getTablePlayerIDsCol gets player IDs from the table without assuming the
// server mutex is held. It acquires a read lock only for the map access and
// then relies on the table's own thread-safety primitives.
func (s *Server) getTablePlayerIDsCol(tableID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	playerIDs := make([]string, 0, len(s.tables[tableID].GetUsers()))
	for _, user := range s.tables[tableID].GetUsers() {
		playerIDs = append(playerIDs, user.ID)
	}
	return playerIDs
}

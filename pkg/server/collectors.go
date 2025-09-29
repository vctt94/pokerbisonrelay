package server

import (
	"fmt"
	"time"

	"github.com/vctt94/pokerbisonrelay/pkg/poker"
	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
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
		// Mirror authoritative winners from table's cached lastShowdown, if any
		if ls := table.GetLastShowdown(); ls != nil && gameSnapshot != nil {
			if len(ls.Winners) > 0 {
				gameSnapshot.Winners = make([]string, len(ls.Winners))
				copy(gameSnapshot.Winners, ls.Winners)
			} else {
				gameSnapshot.Winners = nil
			}
		}
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
				snapshot.HasFolded = player.GetCurrentStateString() == "FOLDED"
				snapshot.IsAllIn = player.GetCurrentStateString() == "ALL_IN"
				snapshot.IsDealer = player.IsDealer
				snapshot.IsTurn = player.IsTurn
				snapshot.GameState = player.GetCurrentStateString()
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

func (s *Server) buildGameEvent(
	eventType pokerrpc.NotificationType,
	tableID string,
	payload interface{},
) (*GameEvent, error) {
	tableSnapshot, err := s.collectTableSnapshot(tableID)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	t := s.tables[tableID]
	s.mu.RUnlock()
	if t == nil {
		return nil, fmt.Errorf("table not found: %s", tableID)
	}

	users := t.GetUsers()
	playerIDs := make([]string, 0, len(users))
	for _, u := range users {
		playerIDs = append(playerIDs, u.ID)
	}

	// Convert poker package payloads to server payloads
	var serverPayload EventPayload
	if payload != nil {
		switch p := payload.(type) {
		case *pokerrpc.Showdown:
			serverPayload = ShowdownPayload{Showdown: p}
		case EventPayload:
			// Already a server payload
			serverPayload = p
		default:
			s.log.Warnf("Unknown payload type %T for event %s on table %s", payload, eventType, tableID)
			serverPayload = nil
		}
	}

	return &GameEvent{
		Type:          eventType,
		TableID:       tableID,
		PlayerIDs:     playerIDs,
		Timestamp:     time.Now(),
		TableSnapshot: tableSnapshot,
		Payload:       serverPayload,
	}, nil
}

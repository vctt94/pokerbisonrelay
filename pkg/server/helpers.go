package server

import (
	"fmt"
	"sort"
	"sync"

	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/pokerbisonrelay/pkg/server/internal/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
		}
	}()
}

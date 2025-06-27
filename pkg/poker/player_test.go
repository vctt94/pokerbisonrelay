package poker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests ensure the state machine preserves fold status correctly.
func TestPlayerStateMachine_FoldRegression(t *testing.T) {
	// Test to prevent regression of the bug where HasFolded was being reset to false
	// by the state machine after setting it to true

	// Create a player
	player := NewPlayer("test-player", "Test Player", 1000)
	require.NotNil(t, player)

	// Player starts in AT_TABLE state
	assert.Equal(t, "AT_TABLE", player.GetGameState())
	assert.False(t, player.HasFolded, "Player should not be folded initially")

	// Simulate player folding during a game
	player.HasFolded = true

	// The critical test: when the state machine runs, it should NOT reset HasFolded to false
	// This simulates what happens when playerStateAtTable is called after a fold
	player.stateMachine.Dispatch(nil)

	// After the state machine dispatch, the player should:
	// 1. Still be marked as folded
	// 2. Have transitioned to FOLDED state
	assert.True(t, player.HasFolded, "Player should remain folded after state machine dispatch")
	assert.Equal(t, "FOLDED", player.GetGameState(), "Player should be in FOLDED state")
}

func TestPlayerStateMachine_FoldStateTransition(t *testing.T) {
	// Test that folding properly transitions the player to FOLDED state

	player := NewPlayer("test-player", "Test Player", 1000)

	// Start in IN_GAME state
	player.SetGameState("IN_GAME")
	assert.Equal(t, "IN_GAME", player.GetGameState())
	assert.False(t, player.HasFolded)

	// Simulate fold action
	player.HasFolded = true

	// State machine should transition to FOLDED
	player.stateMachine.Dispatch(nil)

	assert.True(t, player.HasFolded, "Player should be folded")
	assert.Equal(t, "FOLDED", player.GetGameState(), "Player should be in FOLDED state")
}

func TestPlayerStateMachine_FoldStatePersistence(t *testing.T) {
	// Test that once in FOLDED state, the fold flag persists through multiple state machine steps

	player := NewPlayer("test-player", "Test Player", 1000)

	// Transition to folded state
	player.HasFolded = true
	player.stateMachine.Dispatch(nil)

	assert.True(t, player.HasFolded, "Player should be folded")
	assert.Equal(t, "FOLDED", player.GetGameState(), "Player should be in FOLDED state")

	// Run state machine multiple times - fold should persist
	for i := 0; i < 5; i++ {
		player.stateMachine.Dispatch(nil)
		assert.True(t, player.HasFolded, "Player should remain folded after dispatch %d", i+1)
		assert.Equal(t, "FOLDED", player.GetGameState(), "Player should remain in FOLDED state after dispatch %d", i+1)
	}
}

func TestPlayerStateMachine_UnfoldTransition(t *testing.T) {
	// Test that clearing the fold flag transitions player out of FOLDED state

	player := NewPlayer("test-player", "Test Player", 1000)

	// Get to folded state
	player.HasFolded = true
	player.stateMachine.Dispatch(nil)
	assert.Equal(t, "FOLDED", player.GetGameState())

	// Clear fold flag (simulate new hand)
	player.HasFolded = false
	player.stateMachine.Dispatch(nil)

	assert.False(t, player.HasFolded, "Player should not be folded")
	assert.Equal(t, "IN_GAME", player.GetGameState(), "Player should be back in IN_GAME state")
}

func TestPlayerStateMachine_FoldFromDifferentStates(t *testing.T) {
	// Test that folding works correctly from all relevant states

	testCases := []struct {
		name         string
		initialState string
	}{
		{"Fold from AT_TABLE", "AT_TABLE"},
		{"Fold from IN_GAME", "IN_GAME"},
		{"Fold from ALL_IN", "ALL_IN"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			player := NewPlayer("test-player", "Test Player", 1000)

			// Set initial state
			player.SetGameState(tc.initialState)
			assert.Equal(t, tc.initialState, player.GetGameState())

			// Fold
			player.HasFolded = true
			player.stateMachine.Dispatch(nil)

			// Should transition to FOLDED
			assert.True(t, player.HasFolded, "Player should be folded")
			assert.Equal(t, "FOLDED", player.GetGameState(), "Player should be in FOLDED state")
		})
	}
}

func TestPlayerStateMachine_AllInDoesNotOverrideFold(t *testing.T) {
	// Test that all-in conditions don't override fold status

	player := NewPlayer("test-player", "Test Player", 100)

	// Set up all-in conditions (balance = 0, has bet)
	player.Balance = 0
	player.HasBet = 100

	// But player has also folded
	player.HasFolded = true

	player.stateMachine.Dispatch(nil)

	// Fold should take precedence over all-in
	assert.True(t, player.HasFolded, "Player should be folded")
	assert.False(t, player.IsAllIn, "Player should not be all-in when folded")
	assert.Equal(t, "FOLDED", player.GetGameState(), "Player should be in FOLDED state")
}

func TestResetForNewHand_ClearsFoldState(t *testing.T) {
	// Test that ResetForNewHand properly clears fold state for a new hand

	player := NewPlayer("test-player", "Test Player", 1000)

	// Fold player
	player.HasFolded = true
	player.stateMachine.Dispatch(nil)
	assert.Equal(t, "FOLDED", player.GetGameState())

	// Reset for new hand
	player.ResetForNewHand(1000)

	// Fold state should be cleared
	assert.False(t, player.HasFolded, "Player should not be folded after new hand reset")
	assert.Equal(t, "IN_GAME", player.GetGameState(), "Player should be in IN_GAME state after reset")
}

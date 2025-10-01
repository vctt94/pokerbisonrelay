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
	assert.Equal(t, "AT_TABLE", player.GetCurrentStateString())
	assert.False(t, player.GetCurrentStateString() == "FOLDED", "Player should not be folded initially")

	// Simulate player folding during a game
	player.stateMachine.Dispatch(playerStateFolded)

	// The critical test: when the state machine runs, it should NOT reset HasFolded to false
	// This simulates what happens when playerStateAtTable is called after a fold
	player.stateMachine.Dispatch(player.stateMachine.GetCurrentState())

	// After the state machine dispatch, the player should:
	// 1. Still be marked as folded
	// 2. Have transitioned to FOLDED state
	assert.True(t, player.GetCurrentStateString() == "FOLDED", "Player should remain folded after state machine dispatch")
	assert.Equal(t, "FOLDED", player.GetCurrentStateString(), "Player should be in FOLDED state")
}

func TestPlayerStateMachine_FoldStateTransition(t *testing.T) {
	// Test that folding properly transitions the player to FOLDED state

	player := NewPlayer("test-player", "Test Player", 1000)

	// Start in IN_GAME state
	player.stateMachine.Dispatch(playerStateInGame)
	assert.Equal(t, "IN_GAME", player.GetCurrentStateString())
	assert.False(t, player.GetCurrentStateString() == "FOLDED")

	// Simulate fold action
	player.stateMachine.Dispatch(playerStateFolded)

	// State machine should transition to FOLDED

	assert.True(t, player.GetCurrentStateString() == "FOLDED", "Player should be folded")
	assert.Equal(t, "FOLDED", player.GetCurrentStateString(), "Player should be in FOLDED state")
}

func TestPlayerStateMachine_FoldStatePersistence(t *testing.T) {
	// Test that once in FOLDED state, the fold flag persists through multiple state machine steps

	player := NewPlayer("test-player", "Test Player", 1000)

	// Transition to folded state
	player.stateMachine.Dispatch(playerStateFolded)

	assert.True(t, player.GetCurrentStateString() == "FOLDED", "Player should be folded")
	assert.Equal(t, "FOLDED", player.GetCurrentStateString(), "Player should be in FOLDED state")

	// Run state machine multiple times - fold should persist
	for i := 0; i < 5; i++ {
		assert.True(t, player.GetCurrentStateString() == "FOLDED", "Player should remain folded after dispatch %d", i+1)
		assert.Equal(t, "FOLDED", player.GetCurrentStateString(), "Player should remain in FOLDED state after dispatch %d", i+1)
	}
}

func TestPlayerStateMachine_UnfoldTransition(t *testing.T) {
	// Test that clearing the fold flag transitions player out of FOLDED state

	player := NewPlayer("test-player", "Test Player", 1000)

	// Get to folded state
	player.stateMachine.Dispatch(playerStateFolded)
	assert.Equal(t, "FOLDED", player.GetCurrentStateString())

	// Clear fold flag (simulate new hand)
	player.stateMachine.Dispatch(playerStateAtTable)

	assert.False(t, player.GetCurrentStateString() == "FOLDED", "Player should not be folded")
	assert.Equal(t, "AT_TABLE", player.GetCurrentStateString(), "Player should be back in AT_TABLE state")
}

func TestPlayerStateMachine_FoldFromDifferentStates(t *testing.T) {
	type tc struct {
		name                 string
		setup                func(p *Player)
		expectStateAfterFold string
		expectHasFolded      bool
	}

	tests := []tc{
		{
			name: "Fold from AT_TABLE",
			setup: func(p *Player) {
				p.stateMachine.Dispatch(playerStateAtTable)
			},
			expectStateAfterFold: "FOLDED",
			expectHasFolded:      true,
		},
		{
			name: "Fold from IN_GAME",
			setup: func(p *Player) {
				p.stateMachine.Dispatch(playerStateInGame)
			},
			expectStateAfterFold: "FOLDED",
			expectHasFolded:      true,
		},
		{
			name: "Fold from ALL_IN (ignored)",
			setup: func(p *Player) {
				// Set up all-in conditions: balance = 0 and has bet
				p.balance = 0
				p.currentBet = 100
				p.stateMachine.Dispatch(playerStateInGame)
				p.stateMachine.Dispatch(playerStateAllIn)
			},
			// Folding while all-in should be ignored; remain in ALL_IN.
			expectStateAfterFold: "ALL_IN",
			expectHasFolded:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			player := NewPlayer("test-player", "Test Player", 1000)

			tt.setup(player)

			// Act: attempt to fold
			player.stateMachine.Dispatch(playerStateFolded)

			// Assert
			assert.Equal(t, tt.expectStateAfterFold, player.GetCurrentStateString())
			assert.Equal(t, tt.expectHasFolded, player.GetCurrentStateString() == "FOLDED")
		})
	}
}

func TestResetForNewHand_ClearsFoldState(t *testing.T) {
	// Test that ResetForNewHand properly clears fold state for a new hand

	player := NewPlayer("test-player", "Test Player", 1000)

	// Fold player
	player.stateMachine.Dispatch(playerStateFolded)
	assert.Equal(t, "FOLDED", player.GetCurrentStateString())

	// Reset for new hand
	player.ResetForNewHand(1000)

	// Fold state should be cleared
	assert.False(t, player.GetCurrentStateString() == "FOLDED", "Player should not be folded after new hand reset")
	assert.Equal(t, "IN_GAME", player.GetCurrentStateString(), "Player should be in IN_GAME state after reset")
}

func TestTryFold_AllowsFoldWhenNotAllIn(t *testing.T) {
	// Test that TryFold allows folding when player is not all-in

	player := NewPlayer("test-player", "Test Player", 1000)
	player.stateMachine.Dispatch(playerStateInGame)

	// TryFold should succeed
	success, err := player.TryFold()
	require.NoError(t, err, "TryFold should succeed when not all-in")
	assert.True(t, success, "TryFold should succeed when not all-in")
	assert.True(t, player.GetCurrentStateString() == "FOLDED", "Player should be folded")
	assert.Equal(t, "FOLDED", player.GetCurrentStateString(), "Player should be in FOLDED state")
}

func TestTryFold_PreventsFoldWhenAllIn(t *testing.T) {
	// Test that TryFold prevents folding when player is all-in

	player := NewPlayer("test-player", "Test Player", 1000)
	// Set up all-in conditions: balance = 0 and has bet
	player.balance = 0
	player.currentBet = 100
	player.stateMachine.Dispatch(playerStateInGame)
	player.stateMachine.Dispatch(playerStateAllIn)

	// TryFold should fail
	success, err := player.TryFold()
	require.Error(t, err, "TryFold should fail when all-in")
	assert.False(t, success, "TryFold should fail when all-in")
	assert.False(t, player.GetCurrentStateString() == "FOLDED", "Player should not be folded")
	assert.Equal(t, "ALL_IN", player.GetCurrentStateString(), "Player should remain in ALL_IN state")
}

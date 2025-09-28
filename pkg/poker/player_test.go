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
				p.SetGameState("AT_TABLE")
			},
			expectStateAfterFold: "FOLDED",
			expectHasFolded:      true,
		},
		{
			name: "Fold from IN_GAME",
			setup: func(p *Player) {
				p.SetGameState("IN_GAME")
			},
			expectStateAfterFold: "FOLDED",
			expectHasFolded:      true,
		},
		{
			name: "Fold from ALL_IN (ignored)",
			setup: func(p *Player) {
				// ALL_IN is a condition inside IN_GAME, not a state.
				p.SetGameState("IN_GAME")
				p.IsAllIn = true // or p.SetAllIn(true) if you have a helper
			},
			// Folding while all-in should be ignored; remain in IN_GAME.
			expectStateAfterFold: "IN_GAME",
			expectHasFolded:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			player := NewPlayer("test-player", "Test Player", 1000)

			tt.setup(player)

			// Act: attempt to fold
			player.HasFolded = true
			player.stateMachine.Dispatch(nil)

			// Assert
			assert.Equal(t, tt.expectStateAfterFold, player.GetGameState())
			assert.Equal(t, tt.expectHasFolded, player.HasFolded)
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

func TestTryFold_AllowsFoldWhenNotAllIn(t *testing.T) {
	// Test that TryFold allows folding when player is not all-in

	player := NewPlayer("test-player", "Test Player", 1000)
	player.SetGameState("IN_GAME")
	player.IsAllIn = false

	// TryFold should succeed
	success := player.TryFold()
	assert.True(t, success, "TryFold should succeed when not all-in")
	assert.True(t, player.HasFolded, "Player should be folded")
	assert.Equal(t, "FOLDED", player.GetGameState(), "Player should be in FOLDED state")
}

func TestTryFold_PreventsFoldWhenAllIn(t *testing.T) {
	// Test that TryFold prevents folding when player is all-in

	player := NewPlayer("test-player", "Test Player", 1000)
	player.SetGameState("IN_GAME")
	player.IsAllIn = true

	// TryFold should fail
	success := player.TryFold()
	assert.False(t, success, "TryFold should fail when all-in")
	assert.False(t, player.HasFolded, "Player should not be folded")
	assert.Equal(t, "IN_GAME", player.GetGameState(), "Player should remain in IN_GAME state")
}

package poker

import (
	"fmt"
	"time"

	"github.com/vctt94/pokerbisonrelay/pkg/statemachine"
)

// PlayerStateFn represents a player state function following Rob Pike's pattern
type PlayerStateFn = statemachine.StateFn[Player]

// Player represents a unified poker player state for both table-level and game-level operations
type Player struct {
	// Identity
	ID   string
	Name string

	// Table-level state
	TableSeat      int  // Seat position at the table
	IsReady        bool // Ready to start/continue games
	IsDisconnected bool // Whether player is disconnected (for game flow control)
	LastAction     time.Time

	// Game-level state (reset between hands)
	Balance         int64 // Current in-game chips balance for active hand
	StartingBalance int64 // Chips balance at start of current hand (for calculations)
	Hand            []Card
	CurrentBet      int64 // Current bet amount in this betting round
	IsDealer        bool
	IsTurn          bool

	// State machine - Rob Pike's pattern
	stateMachine *statemachine.StateMachine[Player]

	// Hand evaluation (populated during showdown)
	HandValue       *HandValue
	HandDescription string
}

// NewPlayer creates a new player with the specified starting poker chips
// balance: starting poker chips for the game (not DCR balance)
func NewPlayer(id, name string, balance int64) *Player {
	p := &Player{
		ID:              id,
		Name:            name,
		Balance:         balance, // Starting poker chips
		StartingBalance: balance,
		TableSeat:       -1,
		Hand:            make([]Card, 0, 2),
		CurrentBet:      0,
		LastAction:      time.Now(),
		IsReady:         false,
		IsDealer:        false,
		IsTurn:          false,
	}

	// Initialize the state machine with first state function
	p.stateMachine = statemachine.NewStateMachine(p, playerStateAtTable)

	return p
}

// State functions following Rob Pike's pattern
// Each state function performs its work and returns the next state function (or nil to terminate)

// playerStateAtTable represents the player being at the table but not in game
func playerStateAtTable(entity *Player) PlayerStateFn {
	// Check if player should transition to folded state during a game
	if entity.GetCurrentStateString() == "FOLDED" {
		// Player has folded during a game, transition to folded state
		return playerStateFolded
	}

	return playerStateAtTable // Stay in this state until external transition
}

// playerStateInGame represents the player actively in a game
func playerStateInGame(entity *Player) PlayerStateFn {
	// Update all-in status based on balance and bet
	if entity.Balance == 0 && entity.CurrentBet > 0 {
		// Player is all-in, transition to all-in state
		return playerStateAllIn
	}

	// If player is all-in, ignore any fold attempts and stay in IN_GAME
	if entity.GetCurrentStateString() == "ALL_IN" {
		return playerStateInGame
	}

	if entity.GetCurrentStateString() == "FOLDED" {
		// Player has folded and is not all-in, transition to folded state
		return playerStateFolded
	}

	return playerStateInGame // Stay in this state
}

// playerStateFolded represents the player having folded
func playerStateFolded(entity *Player) PlayerStateFn {
	// Check if player is all-in - if so, ignore the fold attempt
	if entity.Balance == 0 && entity.CurrentBet > 0 {
		// Player is all-in, cannot fold, return to all-in state
		return playerStateAllIn
	}

	// Check if player should transition out of folded state (e.g., new hand started)
	if entity.GetCurrentStateString() != "FOLDED" {
		// Player is no longer folded, transition back to in-game
		return playerStateInGame
	}

	return playerStateFolded // Stay folded
}

// playerStateAllIn represents the player being all-in
func playerStateAllIn(entity *Player) PlayerStateFn {
	// All-in players cannot fold - ignore any fold attempts
	// This prevents the state machine from transitioning to folded when all-in

	if entity.Balance > 0 {
		// Player is no longer all-in (e.g., won chips or new hand), transition back to in-game
		return playerStateInGame
	}

	return playerStateAllIn // Stay all-in
}

// playerStateLeft represents the player having left the table
func playerStateLeft(entity *Player) PlayerStateFn {
	return nil // Terminal state - return nil to end state machine
}

// ensureStateMachine ensures the state machine is initialized, panics if not
// This should only be called during normal operation, never during initialization
func (p *Player) ensureStateMachine() {
	if p.stateMachine == nil {
		panic(fmt.Sprintf("Player %s state machine not initialized - this indicates a bug in player creation", p.ID))
	}
}

// ResetForNewHand resets the player's game-level state for a new hand while preserving table-level state
func (p *Player) ResetForNewHand(startingChips int64) {
	// Clear hand completely - create new slice to ensure old references are lost
	p.Hand = make([]Card, 0, 2)
	p.Balance = startingChips
	p.StartingBalance = startingChips
	p.CurrentBet = 0
	p.IsDealer = false
	p.IsTurn = false
	p.HandValue = nil
	p.HandDescription = ""
	p.LastAction = time.Now()

	// Transition to IN_GAME state
	p.ensureStateMachine()
	p.stateMachine.Dispatch(playerStateInGame)
}

// SetGameState updates the player's game state using the new state machine
func (p *Player) SetGameState(stateName string) {
	p.ensureStateMachine()

	var newState PlayerStateFn

	switch stateName {
	case "AT_TABLE":
		newState = playerStateAtTable
	case "IN_GAME":
		newState = playerStateInGame
	case "FOLDED":
		newState = playerStateFolded
	case "ALL_IN":
		newState = playerStateAllIn
	case "LEFT":
		newState = playerStateLeft
	default:
		return // Unknown state, don't transition
	}

	p.stateMachine.Dispatch(newState)

}

// GetGameState returns a string representation of the current state
func (p *Player) GetCurrentStateString() string {
	if p.stateMachine == nil {
		return "UNINITIALIZED"
	}

	currentState := p.stateMachine.GetCurrentState()
	if currentState == nil {
		return "LEFT"
	}

	// Use function pointer comparison to determine state
	switch fmt.Sprintf("%p", currentState) {
	case fmt.Sprintf("%p", playerStateAtTable):
		return "AT_TABLE"
	case fmt.Sprintf("%p", playerStateInGame):
		return "IN_GAME"
	case fmt.Sprintf("%p", playerStateAllIn):
		return "ALL_IN"
	case fmt.Sprintf("%p", playerStateFolded):
		return "FOLDED"
	case fmt.Sprintf("%p", playerStateLeft):
		return "LEFT"
	default:
		return "UNKNOWN"
	}
}

// TryFold attempts to fold the player, returning true if successful, false if not allowed
// This method enforces the rule that players cannot fold while all-in
func (p *Player) TryFold() bool {
	// Check if player is all-in - if so, fold is not allowed
	if p.Balance == 0 && p.CurrentBet > 0 {
		return false
	}

	// Set the fold flag and let the state machine handle the transition
	p.ensureStateMachine()
	p.stateMachine.Dispatch(playerStateFolded)

	return true
}

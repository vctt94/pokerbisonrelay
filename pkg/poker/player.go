package poker

import (
	"fmt"
	"time"

	"github.com/vctt94/poker-bisonrelay/pkg/statemachine"
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
	HasBet          int64 // Current bet amount in this betting round

	// State machine - Rob Pike's pattern
	stateMachine *statemachine.StateMachine[Player]

	// Game state flags
	HasFolded bool
	IsAllIn   bool
	IsDealer  bool
	IsTurn    bool

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
		HasBet:          0,
		LastAction:      time.Now(),
		IsReady:         false,
		IsDealer:        false,
		IsTurn:          false,
	}

	// Initialize the state machine with first state function
	p.stateMachine = statemachine.NewStateMachine(p, playerStateAtTable)

	// Set initial flags for AT_TABLE state
	p.HasFolded = false
	p.IsAllIn = false

	return p
}

// State functions following Rob Pike's pattern
// Each state function performs its work and returns the next state function (or nil to terminate)

// playerStateAtTable represents the player being at the table but not in game
func playerStateAtTable(entity *Player, callback func(stateName string, event statemachine.StateEvent)) PlayerStateFn {
	// Check if player should transition to folded state during a game
	if entity.HasFolded {
		// Player has folded during a game, transition to folded state
		if callback != nil {
			callback("AT_TABLE", statemachine.StateExited)
		}
		return playerStateFolded
	}

	if callback != nil {
		callback("AT_TABLE", statemachine.StateEntered)
	}
	return playerStateAtTable // Stay in this state until external transition
}

// playerStateInGame represents the player actively in a game
func playerStateInGame(entity *Player, callback func(stateName string, event statemachine.StateEvent)) PlayerStateFn {
	// Check current conditions and transition if necessary BEFORE setting flags
	if entity.HasFolded {
		// Player has folded, transition to folded state
		if callback != nil {
			callback("IN_GAME", statemachine.StateExited)
		}
		return playerStateFolded
	}

	if entity.Balance == 0 && entity.HasBet > 0 {
		// Player is all-in, transition to all-in state
		if callback != nil {
			callback("IN_GAME", statemachine.StateExited)
		}
		return playerStateAllIn
	}

	// Ensure flags are correct for this state
	entity.HasFolded = false
	entity.IsAllIn = false

	if callback != nil {
		callback("IN_GAME", statemachine.StateEntered)
	}
	return playerStateInGame // Stay in this state
}

// playerStateFolded represents the player having folded
func playerStateFolded(entity *Player, callback func(stateName string, event statemachine.StateEvent)) PlayerStateFn {
	// Check if player should transition out of folded state (e.g., new hand started)
	if !entity.HasFolded {
		// Player is no longer folded, transition back to in-game
		if callback != nil {
			callback("FOLDED", statemachine.StateExited)
		}
		return playerStateInGame
	}

	// Ensure flags are correct for this state
	entity.HasFolded = true
	entity.IsAllIn = false

	if callback != nil {
		callback("FOLDED", statemachine.StateEntered)
	}
	return playerStateFolded // Stay folded
}

// playerStateAllIn represents the player being all-in
func playerStateAllIn(entity *Player, callback func(stateName string, event statemachine.StateEvent)) PlayerStateFn {
	// Check if player should transition out of all-in state
	if entity.HasFolded {
		// Player has folded, transition to folded state
		if callback != nil {
			callback("ALL_IN", statemachine.StateExited)
		}
		return playerStateFolded
	}

	if entity.Balance > 0 {
		// Player is no longer all-in (e.g., won chips or new hand), transition back to in-game
		if callback != nil {
			callback("ALL_IN", statemachine.StateExited)
		}
		return playerStateInGame
	}

	// Ensure flags are correct for this state
	entity.HasFolded = false
	entity.IsAllIn = true

	if callback != nil {
		callback("ALL_IN", statemachine.StateEntered)
	}
	return playerStateAllIn // Stay all-in
}

// playerStateLeft represents the player having left the table
func playerStateLeft(entity *Player, callback func(stateName string, event statemachine.StateEvent)) PlayerStateFn {
	entity.HasFolded = false
	entity.IsAllIn = false

	if callback != nil {
		callback("LEFT", statemachine.StateEntered)
	}
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
	p.HasBet = 0
	p.IsDealer = false
	p.IsTurn = false
	p.HandValue = nil
	p.HandDescription = ""
	p.LastAction = time.Now()

	// Transition to IN_GAME state
	p.ensureStateMachine()
	p.stateMachine.SetState(playerStateInGame)
	p.HasFolded = false
	p.IsAllIn = false
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

	p.stateMachine.SetState(newState)

	// Update flags based on new state - this will also be handled by the state functions
	switch stateName {
	case "AT_TABLE", "IN_GAME":
		p.HasFolded = false
		p.IsAllIn = false
	case "FOLDED":
		p.HasFolded = true
		p.IsAllIn = false
	case "ALL_IN":
		p.HasFolded = false
		p.IsAllIn = true
	case "LEFT":
		p.HasFolded = false
		p.IsAllIn = false
	}
}

// GetGameState returns a string representation of the current state
func (p *Player) GetGameState() string {
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
	case fmt.Sprintf("%p", playerStateFolded):
		return "FOLDED"
	case fmt.Sprintf("%p", playerStateAllIn):
		return "ALL_IN"
	case fmt.Sprintf("%p", playerStateLeft):
		return "LEFT"
	default:
		return "UNKNOWN"
	}
}

// IsActiveInGame returns true if the player is actively participating in the current hand
func (p *Player) IsActiveInGame() bool {
	if p.stateMachine == nil {
		return false
	}

	currentState := p.stateMachine.GetCurrentState()
	return fmt.Sprintf("%p", currentState) == fmt.Sprintf("%p", playerStateInGame) ||
		fmt.Sprintf("%p", currentState) == fmt.Sprintf("%p", playerStateAllIn)
}

// IsAtTable returns true if the player is at the table (regardless of game state)
func (p *Player) IsAtTable() bool {
	if p.stateMachine == nil {
		return false
	}

	currentState := p.stateMachine.GetCurrentState()
	return fmt.Sprintf("%p", currentState) != fmt.Sprintf("%p", playerStateLeft)
}

// GetHandString returns a string representation of the player's hand
func (p *Player) GetHandString() string {
	if len(p.Hand) == 0 {
		return "No cards"
	}

	str := ""
	for i, card := range p.Hand {
		if i > 0 {
			str += " "
		}
		str += card.String()
	}
	return str
}

// GetStatus returns a string representation of the player's status
func (p *Player) GetStatus() string {
	status := fmt.Sprintf("Player %s:\n", p.Name)
	status += fmt.Sprintf("Game Chips: %d\n", p.Balance)
	status += fmt.Sprintf("Current Bet: %d chips\n", p.HasBet)
	status += fmt.Sprintf("Hand: %s\n", p.GetHandString())
	status += fmt.Sprintf("State: %s\n", p.GetGameState())
	if p.HasFolded {
		status += "Status: Folded\n"
	} else {
		status += "Status: Active\n"
	}
	return status
}

package poker

import (
	"fmt"
	"time"
)

// playerStateFn represents a state function for a player, similar to Rob Pike's lexical scanning approach
type playerStateFn func(*Player) playerStateFn

// PlayerState represents the current state identifier for comparisons
type PlayerState int

const (
	PlayerState_AT_TABLE PlayerState = iota
	PlayerState_IN_GAME
	PlayerState_FOLDED
	PlayerState_ALL_IN
	PlayerState_LEFT
)

// Player state functions - these handle state transitions and logic
func playerStateAtTable(p *Player) playerStateFn {
	// Player is waiting for a game to start
	p.stateID = PlayerState_AT_TABLE
	p.HasFolded = false
	p.IsAllIn = false
	return nil // Stay in this state until explicitly transitioned
}

func playerStateInGame(p *Player) playerStateFn {
	// Player is actively participating in the game
	p.stateID = PlayerState_IN_GAME
	p.HasFolded = false
	p.IsAllIn = false
	return nil // Stay in this state until explicitly transitioned
}

func playerStateFolded(p *Player) playerStateFn {
	// Player has folded, mark the compatibility flags
	p.stateID = PlayerState_FOLDED
	p.HasFolded = true
	p.IsAllIn = false
	return nil // Stay folded until next hand
}

func playerStateAllIn(p *Player) playerStateFn {
	// Player is all-in, mark the compatibility flags
	p.stateID = PlayerState_ALL_IN
	p.IsAllIn = true
	p.HasFolded = false
	return nil // Stay all-in until hand resolves
}

func playerStateLeft(p *Player) playerStateFn {
	// Player has left, cleanup state
	p.stateID = PlayerState_LEFT
	p.HasFolded = false
	p.IsAllIn = false
	return nil // Terminal state
}

// Player represents a unified poker player state for both table-level and game-level operations
type Player struct {
	// Identity
	ID   string
	Name string

	// Table-level state
	AccountBalance int64 // DCR account balance (in atoms) - persistent across games
	TableSeat      int   // Seat position at the table
	IsReady        bool  // Ready to start/continue games
	LastAction     time.Time

	// Game-level state (reset between hands)
	Balance         int64 // Current in-game chips balance for active hand
	StartingBalance int64 // Chips balance at start of current hand (for calculations)
	Hand            []Card
	HasBet          int64 // Current bet amount in this betting round

	// State function and identifier
	currentState playerStateFn
	stateID      PlayerState

	// Game state flags (kept for API compatibility)
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

	// Initialize with the at-table state
	p.transitionTo(playerStateAtTable)
	return p
}

// ResetForNewHand resets the player's game-level state for a new hand while preserving table-level state
func (p *Player) ResetForNewHand(startingChips int64) {
	p.Hand = p.Hand[:0]
	p.Balance = startingChips
	p.StartingBalance = startingChips
	p.HasBet = 0
	p.transitionTo(playerStateInGame)
	p.IsDealer = false
	p.IsTurn = false
	p.HandValue = nil
	p.HandDescription = ""
	p.LastAction = time.Now()
}

// transitionTo transitions the player to a new state, following the stateFn pattern
func (p *Player) transitionTo(newState playerStateFn) {
	p.currentState = newState
	if newState != nil {
		// Execute the state function - this may return the next state to transition to
		nextState := p.currentState(p)
		if nextState != nil {
			// If the state function wants to transition to another state, do it
			p.transitionTo(nextState)
		}
	}
}

// SetGameState updates the player's game state using the state function pattern
func (p *Player) SetGameState(stateName string) {
	switch stateName {
	case "AT_TABLE":
		p.transitionTo(playerStateAtTable)
	case "IN_GAME":
		p.transitionTo(playerStateInGame)
	case "FOLDED":
		p.transitionTo(playerStateFolded)
	case "ALL_IN":
		p.transitionTo(playerStateAllIn)
	case "LEFT":
		p.transitionTo(playerStateLeft)
	}
}

// GetGameState returns a string representation of the current state for compatibility
func (p *Player) GetGameState() string {
	switch p.stateID {
	case PlayerState_AT_TABLE:
		return "AT_TABLE"
	case PlayerState_IN_GAME:
		return "IN_GAME"
	case PlayerState_FOLDED:
		return "FOLDED"
	case PlayerState_ALL_IN:
		return "ALL_IN"
	case PlayerState_LEFT:
		return "LEFT"
	default:
		return "UNKNOWN"
	}
}

// IsActiveInGame returns true if the player is actively participating in the current hand
func (p *Player) IsActiveInGame() bool {
	return p.stateID == PlayerState_IN_GAME || p.stateID == PlayerState_ALL_IN
}

// IsAtTable returns true if the player is at the table (regardless of game state)
func (p *Player) IsAtTable() bool {
	return p.stateID != PlayerState_LEFT
}

// Bet places a bet for the player
func (p *Player) Bet(amount int64) error {
	if amount > p.Balance {
		return fmt.Errorf("insufficient balance")
	}

	p.Balance -= amount
	p.HasBet += amount
	p.LastAction = time.Now()

	// Check if player is now all-in
	if p.Balance == 0 {
		p.transitionTo(playerStateAllIn)
	}

	return nil
}

// Fold makes the player fold their hand
func (p *Player) Fold() {
	p.transitionTo(playerStateFolded)
	p.LastAction = time.Now()
}

// Reset resets the player's hand and betting state (legacy method for compatibility)
func (p *Player) Reset() {
	p.Hand = p.Hand[:0]
	p.HasFolded = false
	p.HasBet = 0
	p.LastAction = time.Now()
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
	status += fmt.Sprintf("Account Balance: %.8f DCR\n", float64(p.AccountBalance)/1e8)
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

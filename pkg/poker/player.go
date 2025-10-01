package poker

import (
	"fmt"
	"time"

	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/pokerbisonrelay/pkg/statemachine"
)

// PlayerStateFn represents a player state function following Rob Pike's pattern
type PlayerStateFn = statemachine.StateFn[Player]

// Player represents a unified poker player state for both table-level and game-level operations
type Player struct {
	// Identity
	id   string
	name string

	// Table-level state
	tableSeat      int  // Seat position at the table
	isReady        bool // Ready to start/continue games
	isDisconnected bool // Whether player is disconnected (for game flow control)
	lastAction     time.Time

	// Game-level state (reset between hands)
	balance         int64 // Current in-game chips balance for active hand
	startingBalance int64 // Chips balance at start of current hand (for calculations)
	hand            []Card
	currentBet      int64 // Current bet amount in this betting round
	isDealer        bool
	isTurn          bool

	// State machine - Rob Pike's pattern
	stateMachine *statemachine.StateMachine[Player]

	// Hand evaluation (populated during showdown)
	handValue       *HandValue
	handDescription string
}

// NewPlayer creates a new player with the specified starting poker chips
// balance: starting poker chips for the game (not DCR balance)
func NewPlayer(id, name string, balance int64) *Player {
	p := &Player{
		id:              id,
		name:            name,
		balance:         balance, // Starting poker chips
		startingBalance: balance,
		tableSeat:       -1,
		hand:            make([]Card, 0, 2),
		currentBet:      0,
		lastAction:      time.Now(),
		isReady:         false,
		isDealer:        false,
		isTurn:          false,
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
	if entity.balance == 0 && entity.currentBet > 0 {
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
	if entity.balance == 0 && entity.currentBet > 0 {
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

	if entity.balance > 0 {
		// Player is no longer all-in (e.g., won chips or new hand), transition back to in-game
		return playerStateInGame
	}

	return playerStateAllIn // Stay all-in
}

// playerStateLeft represents the player having left the table
func playerStateLeft(entity *Player) PlayerStateFn {
	return nil // Terminal state - return nil to end state machine
}

// ResetForNewHand resets the player's game-level state for a new hand while preserving table-level state
func (p *Player) ResetForNewHand(startingChips int64) error {
	// Clear hand completely - create new slice to ensure old references are lost
	p.hand = make([]Card, 0, 2)
	p.balance = startingChips
	p.startingBalance = startingChips
	p.currentBet = 0
	p.isDealer = false
	p.isTurn = false
	p.handValue = nil
	p.handDescription = ""
	p.lastAction = time.Now()

	// Transition to IN_GAME state
	if p.stateMachine == nil {
		return fmt.Errorf("player state machine not initialized")
	}
	p.stateMachine.Dispatch(playerStateInGame)
	return nil
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
func (p *Player) TryFold() (bool, error) {
	// Check if player is all-in - if so, fold is not allowed
	if p.balance == 0 && p.currentBet > 0 {
		return false, fmt.Errorf("player is all-in")
	}

	// Set the fold flag and let the state machine handle the transition
	if p.stateMachine == nil {
		return false, fmt.Errorf("player state machine not initialized")
	}
	p.stateMachine.Dispatch(playerStateFolded)

	return true, nil
}

// Marshal converts the Player to gRPC Player for external access
func (p *Player) Marshal() *pokerrpc.Player {
	// Convert []Card to []*Card for gRPC
	grpcHand := make([]*pokerrpc.Card, len(p.hand))
	for i, card := range p.hand {
		grpcHand[i] = &pokerrpc.Card{
			Suit:  string(card.suit),
			Value: string(card.value),
		}
	}

	return &pokerrpc.Player{
		Id:              p.id,
		Name:            p.name,
		Balance:         p.balance,
		Hand:            grpcHand,
		CurrentBet:      p.currentBet,
		Folded:          p.GetCurrentStateString() == "FOLDED",
		IsTurn:          p.isTurn,
		IsAllIn:         p.GetCurrentStateString() == "ALL_IN",
		IsDealer:        p.isDealer,
		IsReady:         p.isReady,
		HandDescription: p.handDescription,
		PlayerState:     p.RPCPlayerState(),
	}
}

// RPCPlayerState maps the internal player state string to the protobuf enum.
func (p *Player) RPCPlayerState() pokerrpc.PlayerState {
	switch p.GetCurrentStateString() {
	case "AT_TABLE":
		return pokerrpc.PlayerState_PLAYER_STATE_AT_TABLE
	case "IN_GAME":
		return pokerrpc.PlayerState_PLAYER_STATE_IN_GAME
	case "ALL_IN":
		return pokerrpc.PlayerState_PLAYER_STATE_ALL_IN
	case "FOLDED":
		return pokerrpc.PlayerState_PLAYER_STATE_FOLDED
	case "LEFT":
		return pokerrpc.PlayerState_PLAYER_STATE_LEFT
	default:
		return pokerrpc.PlayerState_PLAYER_STATE_AT_TABLE
	}
}

// Unmarshal updates the Player from gRPC Player
func (p *Player) Unmarshal(grpcPlayer *pokerrpc.Player) {
	p.id = grpcPlayer.Id
	p.name = grpcPlayer.Name
	p.balance = grpcPlayer.Balance
	p.currentBet = grpcPlayer.CurrentBet
	p.isTurn = grpcPlayer.IsTurn
	p.isDealer = grpcPlayer.IsDealer
	p.isReady = grpcPlayer.IsReady
	p.handDescription = grpcPlayer.HandDescription

	// Convert []*Card to []Card for internal use
	p.hand = make([]Card, len(grpcPlayer.Hand))
	for i, grpcCard := range grpcPlayer.Hand {
		p.hand[i] = Card{
			suit:  Suit(grpcCard.Suit),
			value: Value(grpcCard.Value),
		}
	}
}

// RestoreState sets the player's state machine to the provided state string.
// The provided state must match the strings returned by GetCurrentStateString().
func (p *Player) RestoreState(state string) error {
	if p.stateMachine == nil {
		return fmt.Errorf("player state machine not initialized")
	}

	switch state {
	case "AT_TABLE":
		p.stateMachine.Dispatch(playerStateAtTable)
	case "IN_GAME":
		p.stateMachine.Dispatch(playerStateInGame)
	case "ALL_IN":
		p.stateMachine.Dispatch(playerStateAllIn)
	case "FOLDED":
		p.stateMachine.Dispatch(playerStateFolded)
	case "LEFT":
		p.stateMachine.Dispatch(playerStateLeft)
	default:
		return fmt.Errorf("unknown player state: %s", state)
	}

	return nil
}

func (p *Player) Hand() []Card {
	return p.hand
}

func (p *Player) HandDescription() string {
	return p.handDescription
}

func (p *Player) ID() string {
	return p.id
}

// Name returns the player's display name
func (p *Player) Name() string {
	return p.name
}

func (p *Player) Balance() int64 {
	return p.balance
}

func (p *Player) IsReady() bool {
	return p.isReady
}

// GetTableSeat returns the table seat (for external access)
func (p *Player) TableSeat() int {
	return p.tableSeat
}

// GetStartingBalance returns the starting balance (for external access)
func (p *Player) StartingBalance() int64 {
	return p.startingBalance
}

// GetIsDisconnected returns the disconnected state (for external access)
func (p *Player) IsDisconnected() bool {
	return p.isDisconnected
}

// GetCurrentBet returns the current bet (for external access)
func (p *Player) CurrentBet() int64 {
	return p.currentBet
}

// SetTableSeat sets the table seat (for external access)
func (p *Player) SetTableSeat(seat int) {
	p.tableSeat = seat
}

// SetStartingBalance sets the starting balance (for external access)
func (p *Player) SetStartingBalance(balance int64) {
	p.startingBalance = balance
}

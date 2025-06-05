package poker

import (
	"math/rand"
	"sync"
	"time"

	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
)

// GameConfig holds configuration for a new game
type GameConfig struct {
	NumPlayers    int
	StartingChips int64 // Fixed number of chips each player starts with
	SmallBlind    int64 // Small blind amount
	BigBlind      int64 // Big blind amount
	Seed          int64 // Optional seed for deterministic games
}

// Game holds the context and data for our poker game
type Game struct {
	// Player management
	players       []*Player // References to the same Player objects in the table
	currentPlayer int
	dealer        int

	// Cards
	deck           *Deck
	communityCards []Card

	// Game state
	potManager *PotManager
	currentBet int64
	round      int
	betRound   int // Tracks which betting round (pre-flop, flop, turn, river)

	// Configuration
	config GameConfig

	// For demonstration purposes
	errorSimulation bool
	maxRounds       int

	mu sync.RWMutex

	// current game phase (pre-flop, flop, turn, river, showdown)
	phase pokerrpc.GamePhase
}

// stateFn is a function that takes a Game and returns the next state function
type stateFn func(*Game) stateFn

// NewGame creates a new poker game with the given configuration
func NewGame(cfg GameConfig) *Game {
	if cfg.NumPlayers < 2 {
		panic("poker: must have at least 2 players")
	}

	// Create a new deck with the given seed (or random if not specified)
	var rng *rand.Rand
	if cfg.Seed != 0 {
		rng = rand.New(rand.NewSource(cfg.Seed))
	} else {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	return &Game{
		players:         make([]*Player, cfg.NumPlayers), // Will be populated by table with shared Player objects
		currentPlayer:   0,
		dealer:          0,
		deck:            NewDeck(rng),
		communityCards:  nil,
		potManager:      NewPotManager(),
		currentBet:      0,
		round:           0,
		betRound:        0,
		config:          cfg,
		errorSimulation: false,
		phase:           pokerrpc.GamePhase_WAITING,
	}
}

// Run executes the state machine until a nil state is returned
func (g *Game) Run() {
	for state := statePreDeal; state != nil; {
		state = state(g)
		time.Sleep(500 * time.Millisecond) // Slow down for demo purposes
	}
}

// statePreDeal prepares the game for a new hand
func statePreDeal(g *Game) stateFn {
	// Reset game state for a new hand
	g.round++

	// Reset the deck, community cards, pot, etc.
	g.deck.Shuffle()
	g.communityCards = []Card{}
	g.potManager = NewPotManager()
	g.currentBet = 0
	g.betRound = 0

	// Rotate dealer position
	g.dealer = (g.dealer + 1) % len(g.players)
	// Don't set currentPlayer here - it will be set correctly in stateBlinds

	// Set phase to PRE_FLOP (game about to start)
	g.phase = pokerrpc.GamePhase_PRE_FLOP

	return stateDeal
}

// stateDeal deals initial cards to players
func stateDeal(g *Game) stateFn {
	// Note: Card dealing is handled by the table layer to maintain
	// consistency with existing game flow. This state is mainly for
	// state machine progression.

	// After dealing (handled externally), move to blinds state
	return stateBlinds
}

// stateBlinds handles posting small and big blinds and sets the current player
func stateBlinds(g *Game) stateFn {
	g.mu.Lock()
	defer g.mu.Unlock()

	numPlayers := len(g.players)
	if numPlayers < 2 {
		return stateEnd
	}

	// Calculate blind positions
	smallBlindPos := (g.dealer + 1) % numPlayers
	bigBlindPos := (g.dealer + 2) % numPlayers

	// For heads-up (2 players), dealer posts small blind
	if numPlayers == 2 {
		smallBlindPos = g.dealer
		bigBlindPos = (g.dealer + 1) % numPlayers
	}

	// Post small blind
	if g.players[smallBlindPos] != nil {
		smallBlindAmount := g.config.SmallBlind
		if smallBlindAmount > g.players[smallBlindPos].Balance {
			return stateEnd // Not enough balance for small blind
		}
		g.players[smallBlindPos].Balance -= smallBlindAmount
		g.players[smallBlindPos].HasBet = smallBlindAmount
		g.potManager.AddBet(smallBlindPos, smallBlindAmount)
	}

	// Post big blind
	if g.players[bigBlindPos] != nil {
		bigBlindAmount := g.config.BigBlind
		if bigBlindAmount > g.players[bigBlindPos].Balance {
			return stateEnd // Not enough balance for big blind
		}
		g.players[bigBlindPos].Balance -= bigBlindAmount
		g.players[bigBlindPos].HasBet = bigBlindAmount
		g.potManager.AddBet(bigBlindPos, bigBlindAmount)
		g.currentBet = bigBlindAmount // Set current bet to big blind amount
	}

	// Set first player to act (after big blind for pre-flop)
	if numPlayers == 2 {
		// In heads-up, small blind acts first pre-flop
		g.currentPlayer = smallBlindPos
	} else {
		// With 3+ players, first to act is after big blind
		g.currentPlayer = (bigBlindPos + 1) % numPlayers
	}

	// Move to pre-flop betting
	return stateBet
}

// stateBet handles a betting round
func stateBet(g *Game) stateFn {

	// Determine next state based on betting round
	switch g.betRound {
	case 0: // Pre-flop complete, move to flop
		g.betRound++
		return stateFlop
	case 1: // Flop betting complete, move to turn
		g.betRound++
		return stateTurn
	case 2: // Turn betting complete, move to river
		g.betRound++
		return stateRiver
	case 3: // River betting complete, move to showdown
		return stateShowdown
	}

	// Should never reach here
	return stateEnd
}

// stateFlop deals the flop (first 3 community cards)
func stateFlop(g *Game) stateFn {
	// Deal 3 cards to community
	for i := 0; i < 3; i++ {
		card, ok := g.deck.Draw()
		if !ok {
			return stateEnd // End game if deck is empty
		}
		g.communityCards = append(g.communityCards, card)
	}

	// Reset bets for new betting round (table handles this)
	g.currentBet = 0
	g.potManager.ResetCurrentBets()

	// Update phase to FLOP
	g.phase = pokerrpc.GamePhase_FLOP

	return stateBet
}

// stateTurn deals the turn (fourth community card)
func stateTurn(g *Game) stateFn {
	// Deal the turn (4th community card)
	card, ok := g.deck.Draw()
	if !ok {
		return stateEnd // End game if deck is empty
	}
	g.communityCards = append(g.communityCards, card)

	// Reset bets for new betting round (table handles this)
	g.currentBet = 0
	g.potManager.ResetCurrentBets()

	// Update phase to TURN
	g.phase = pokerrpc.GamePhase_TURN

	return stateBet
}

// stateRiver deals the river (fifth community card)
func stateRiver(g *Game) stateFn {
	// Deal the river (5th community card)
	card, ok := g.deck.Draw()
	if !ok {
		return stateEnd // End game if deck is empty
	}
	g.communityCards = append(g.communityCards, card)

	// Reset bets for new betting round (table handles this)
	g.currentBet = 0
	g.potManager.ResetCurrentBets()

	// Update phase to RIVER
	g.phase = pokerrpc.GamePhase_RIVER

	return stateBet
}

// stateShowdown determines the winner of the hand
func stateShowdown(g *Game) stateFn {
	g.mu.Lock()
	defer g.mu.Unlock()
	// Update phase to SHOWDOWN
	g.phase = pokerrpc.GamePhase_SHOWDOWN

	// Return to pre-deal to start a new hand
	return statePreDeal
}

// stateEnd terminates the game
func stateEnd(g *Game) stateFn {
	return nil
}

// GetPot returns the total pot amount
func (g *Game) GetPot() int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return g.potManager.GetTotalPot()
}

// DealCards deals cards to all players
func (g *Game) DealCards() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.currentBet = 0
	return nil
}

// StateFlop deals the flop (3 community cards)
func (g *Game) StateFlop() {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Deal 3 cards to community
	for i := 0; i < 3; i++ {
		card, ok := g.deck.Draw()
		if !ok {
			// Handle error
			return
		}
		g.communityCards = append(g.communityCards, card)
	}

	// Update phase
	g.phase = pokerrpc.GamePhase_FLOP
}

// StateTurn deals the turn (1 community card)
func (g *Game) StateTurn() {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Deal 1 card to community
	card, ok := g.deck.Draw()
	if !ok {
		// Handle error
		return
	}
	g.communityCards = append(g.communityCards, card)

	g.phase = pokerrpc.GamePhase_TURN
}

// StateRiver deals the river (1 community card)
func (g *Game) StateRiver() {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Deal 1 card to community
	card, ok := g.deck.Draw()
	if !ok {
		// Handle error
		return
	}
	g.communityCards = append(g.communityCards, card)

	g.phase = pokerrpc.GamePhase_RIVER
}

// GetPhase returns the current phase of the game.
func (g *Game) GetPhase() pokerrpc.GamePhase {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.phase
}

// GetCurrentBet returns the current bet amount
func (g *Game) GetCurrentBet() int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return g.currentBet
}

// AddToPot adds the specified amount to the pot
func (g *Game) AddToPot(amount int64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.potManager.AddBet(g.currentPlayer, amount)
}

// AddToPotForPlayer adds the specified amount to the pot for a specific player
func (g *Game) AddToPotForPlayer(playerIndex int, amount int64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.potManager.AddBet(playerIndex, amount)
}

// GetCommunityCards returns a copy of the community cards slice.
func (g *Game) GetCommunityCards() []Card {
	g.mu.RLock()
	defer g.mu.RUnlock()
	cards := make([]Card, len(g.communityCards))
	copy(cards, g.communityCards)
	return cards
}

// GetPlayers returns the game players slice
func (g *Game) GetPlayers() []*Player {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.players
}

// GetCurrentPlayer returns the index of the current player to act
func (g *Game) GetCurrentPlayer() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.currentPlayer
}

// GetCurrentPlayerObject returns the current player object
func (g *Game) GetCurrentPlayerObject() *Player {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.currentPlayer >= 0 && g.currentPlayer < len(g.players) {
		return g.players[g.currentPlayer]
	}
	return nil
}

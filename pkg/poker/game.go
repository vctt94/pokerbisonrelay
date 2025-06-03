package poker

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
)

// GameConfig holds configuration for a new game
type GameConfig struct {
	NumPlayers int
	Seed       int64 // Optional seed for deterministic games
}

// Game holds the context and data for our poker game
type Game struct {
	// Players and their hands
	players       []*Player
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

	players := make([]*Player, cfg.NumPlayers)
	for i := range players {
		players[i] = &Player{
			Name:      fmt.Sprintf("Player%d", i+1),
			Balance:   100, // Starting chips
			HasFolded: false,
			HasBet:    0,
		}
	}

	// Create a new deck with the given seed (or random if not specified)
	var rng *rand.Rand
	if cfg.Seed != 0 {
		rng = rand.New(rand.NewSource(cfg.Seed))
	} else {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	return &Game{
		players:         players,
		dealer:          0,
		currentPlayer:   0,
		deck:            NewDeck(rng),
		communityCards:  nil,
		potManager:      NewPotManager(),
		currentBet:      0,
		round:           0,
		betRound:        0,
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

	// Reset player hands and status
	for i := range g.players {
		g.players[i].Hand = []Card{}
		g.players[i].HasFolded = false
		g.players[i].HasBet = 0
		g.players[i].HandValue = nil
		g.players[i].HandDescription = ""
	}

	// Rotate dealer position
	g.dealer = (g.dealer + 1) % len(g.players)
	g.currentPlayer = (g.dealer + 1) % len(g.players)

	// Set phase to PRE_FLOP (game about to start)
	g.phase = pokerrpc.GamePhase_PRE_FLOP

	return stateDeal
}

// stateDeal deals initial cards to players
func stateDeal(g *Game) stateFn {
	// Deal 2 cards to each player
	for i := 0; i < 2; i++ {
		for p := range g.players {
			card, ok := g.deck.Draw()
			if !ok {
				return stateEnd // End game if deck is empty
			}
			g.players[p].Hand = append(g.players[p].Hand, card)
		}
	}

	// Move to first betting round (pre-flop)
	return stateBet
}

// stateBet handles a betting round
func stateBet(g *Game) stateFn {
	// Simulate betting actions
	for i := 0; i < len(g.players); i++ {
		playerIdx := (g.currentPlayer + i) % len(g.players)
		player := g.players[playerIdx]

		if player.HasFolded {
			continue
		}

		// Simulated bet actions (in a real game, this would be user input or AI)
		action := "call"
		if i%3 == 0 {
			action = "raise"
		}

		switch action {
		case "fold":
			player.HasFolded = true
		case "call":
			betAmount := g.currentBet - player.HasBet
			player.Balance -= betAmount
			g.potManager.AddBet(playerIdx, betAmount)
			player.HasBet = g.currentBet
		case "raise":
			raiseAmount := int64(10)
			g.currentBet += raiseAmount
			betAmount := g.currentBet - player.HasBet
			player.Balance -= betAmount
			g.potManager.AddBet(playerIdx, betAmount)
			player.HasBet = g.currentBet
		}
	}

	// Check for error simulation - jump back to betting if needed
	if g.errorSimulation && g.betRound == 1 {
		g.errorSimulation = false // Reset so we don't loop infinitely
		return stateBet           // Jump back to the same state
	}

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

// stateFlop deals the flop (first three community cards)
func stateFlop(g *Game) stateFn {
	// Deal 3 community cards (the flop)
	for i := 0; i < 3; i++ {
		card, ok := g.deck.Draw()
		if !ok {
			return stateEnd // End game if deck is empty
		}
		g.communityCards = append(g.communityCards, card)
	}

	// Reset bets for new betting round
	for i := range g.players {
		g.players[i].HasBet = 0
	}
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

	// Reset bets for new betting round
	for i := range g.players {
		g.players[i].HasBet = 0
	}
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

	// Reset bets for new betting round
	for i := range g.players {
		g.players[i].HasBet = 0
	}
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

	// Evaluate each player's hand
	for _, player := range g.players {
		if !player.HasFolded {
			// Evaluate hand for this player
			handValue := EvaluateHand(player.Hand, g.communityCards)
			player.HandValue = &handValue
			player.HandDescription = GetHandDescription(handValue)
		}
	}

	// Check for any uncalled bets and return them
	g.potManager.ReturnUncalledBet(g.players)

	// Create side pots if needed
	g.potManager.CreateSidePots(g.players)

	// Distribute pots to winners
	g.potManager.DistributePots(g.players)

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

	for i := range g.players {
		g.players[i].Hand = make([]Card, 2)
		for j := 0; j < 2; j++ {
			card, ok := g.deck.Draw()
			if !ok {
				return fmt.Errorf("deck is empty")
			}
			g.players[i].Hand[j] = card
		}
		g.players[i].HasFolded = false
		g.players[i].HasBet = 0
	}
	g.currentBet = 0
	return nil
}

// GetActivePlayers returns the number of players who haven't folded
func (g *Game) GetActivePlayers() int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	count := 0
	for _, player := range g.players {
		if !player.HasFolded {
			count++
		}
	}
	return count
}

// GetPlayerHand returns a player's hand
func (g *Game) GetPlayerHand(playerID string) ([]Card, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	for _, player := range g.players {
		if player.Name == playerID {
			return player.Hand, nil
		}
	}
	return nil, fmt.Errorf("player not found")
}

// IsPlayerActive checks if a player is still in the hand
func (g *Game) IsPlayerActive(playerID string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	for _, player := range g.players {
		if player.Name == playerID && !player.HasFolded {
			return true
		}
	}
	return false
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

package poker

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/poker-bisonrelay/pkg/statemachine"
)

// GameStateFn represents a game state function following Rob Pike's pattern
type GameStateFn = statemachine.StateFn[Game]

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
	// Player management - references to table users converted to players
	players       []*Player // Internal player objects managed by game
	currentPlayer int
	dealer        int

	// Cards
	deck           *Deck
	communityCards []Card

	// Game state
	potManager     *PotManager
	currentBet     int64
	round          int
	betRound       int // Tracks which betting round (pre-flop, flop, turn, river)
	actionsInRound int // Track actions in current betting round

	// Configuration
	config GameConfig

	// For demonstration purposes
	errorSimulation bool
	maxRounds       int

	mu sync.RWMutex

	// current game phase (pre-flop, flop, turn, river, showdown)
	phase pokerrpc.GamePhase

	// Winner tracking - set after showdown is complete
	winners []string

	// State machine - Rob Pike's pattern
	stateMachine *statemachine.StateMachine[Game]
}

// NewGame creates a new poker game with the given configuration
// Players are managed by the Table, not the Game
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

	g := &Game{
		players:         make([]*Player, 0, cfg.NumPlayers), // Empty slice, Table will populate
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
		phase:           pokerrpc.GamePhase_NEW_HAND_DEALING,
	}

	// Initialize state machine with first state function
	g.stateMachine = statemachine.NewStateMachine(g, stateNewHandDealing)

	return g
}

// State functions following Rob Pike's pattern
// Each state function performs its work and returns the next state function (or nil to terminate)

// stateNewHandDealing handles the NEW_HAND_DEALING phase
func stateNewHandDealing(entity *Game, callback func(stateName string, event statemachine.StateEvent)) GameStateFn {
	// This state is primarily managed by the table layer
	// The table handles card dealing and blind posting, then transitions to PRE_FLOP
	// This state function is mainly for completeness in the state machine
	entity.phase = pokerrpc.GamePhase_NEW_HAND_DEALING
	if callback != nil {
		callback("NEW_HAND_DEALING", statemachine.StateEntered)
	}
	return statePreDeal
}

// statePreDeal prepares the game for a new hand
func statePreDeal(entity *Game, callback func(stateName string, event statemachine.StateEvent)) GameStateFn {
	// Reset game state for a new hand
	entity.round++

	// Reset the deck, community cards, pot, etc.
	entity.deck.Shuffle()
	entity.communityCards = []Card{}
	entity.potManager = NewPotManager()
	entity.currentBet = 0
	entity.betRound = 0

	// Rotate dealer position
	entity.dealer = (entity.dealer + 1) % len(entity.players)
	// Don't set currentPlayer here - it will be set correctly in stateBlinds

	// Set phase to PRE_FLOP (game about to start)
	entity.phase = pokerrpc.GamePhase_PRE_FLOP

	if callback != nil {
		callback("PRE_DEAL", statemachine.StateEntered)
	}

	return stateDeal
}

// stateDeal deals initial cards to players
func stateDeal(entity *Game, callback func(stateName string, event statemachine.StateEvent)) GameStateFn {
	// Note: Card dealing is handled by the table layer to maintain
	// consistency with existing game flow. This state is mainly for
	// state machine progression.

	if callback != nil {
		callback("DEAL", statemachine.StateEntered)
	}

	// After dealing (handled externally), move to blinds state
	return stateBlinds
}

// stateBlinds handles posting small and big blinds and sets the current player
func stateBlinds(entity *Game, callback func(stateName string, event statemachine.StateEvent)) GameStateFn {
	numPlayers := len(entity.players)
	if numPlayers < 2 {
		if callback != nil {
			callback("END", statemachine.StateEntered)
		}
		return stateEnd
	}

	// Calculate blind positions
	smallBlindPos := (entity.dealer + 1) % numPlayers
	bigBlindPos := (entity.dealer + 2) % numPlayers

	// For heads-up (2 players), dealer posts small blind
	if numPlayers == 2 {
		smallBlindPos = entity.dealer
		bigBlindPos = (entity.dealer + 1) % numPlayers
	}

	// Post small blind
	if entity.players[smallBlindPos] != nil {
		smallBlindAmount := entity.config.SmallBlind
		if smallBlindAmount > entity.players[smallBlindPos].Balance {
			if callback != nil {
				callback("END", statemachine.StateEntered)
			}
			return stateEnd // Not enough balance for small blind
		}
		entity.players[smallBlindPos].Balance -= smallBlindAmount
		entity.players[smallBlindPos].HasBet = smallBlindAmount
		entity.potManager.AddBet(smallBlindPos, smallBlindAmount)
	}

	// Post big blind
	if entity.players[bigBlindPos] != nil {
		bigBlindAmount := entity.config.BigBlind
		if bigBlindAmount > entity.players[bigBlindPos].Balance {
			if callback != nil {
				callback("END", statemachine.StateEntered)
			}
			return stateEnd // Not enough balance for big blind
		}
		entity.players[bigBlindPos].Balance -= bigBlindAmount
		entity.players[bigBlindPos].HasBet = bigBlindAmount
		entity.potManager.AddBet(bigBlindPos, bigBlindAmount)
		entity.currentBet = bigBlindAmount // Set current bet to big blind amount
	}

	// Set first player to act (after big blind for pre-flop)
	if numPlayers == 2 {
		// In heads-up, small blind acts first pre-flop
		entity.currentPlayer = smallBlindPos
	} else {
		// With 3+ players, first to act is after big blind
		entity.currentPlayer = (bigBlindPos + 1) % numPlayers
	}

	if callback != nil {
		callback("BLINDS", statemachine.StateEntered)
	}

	// Move to pre-flop betting
	return statePreFlop
}

// statePreFlop handles the pre-flop betting round logic
func statePreFlop(entity *Game, callback func(stateName string, event statemachine.StateEvent)) GameStateFn {
	// This is a betting round - handled by external logic
	// Based on betting completion, determine next state
	if callback != nil {
		callback("PRE_FLOP", statemachine.StateEntered)
	}

	switch entity.betRound {
	case 0: // Pre-flop complete, move to flop
		entity.betRound++
		return stateFlop
	default:
		// Still in pre-flop betting - stay in this state
		return statePreFlop
	}
}

// stateFlop deals the flop (first 3 community cards)
func stateFlop(entity *Game, callback func(stateName string, event statemachine.StateEvent)) GameStateFn {
	// Deal 3 cards to community
	for i := 0; i < 3; i++ {
		card, ok := entity.deck.Draw()
		if !ok {
			if callback != nil {
				callback("END", statemachine.StateEntered)
			}
			return stateEnd // End game if deck is empty
		}
		entity.communityCards = append(entity.communityCards, card)
	}

	// Reset bets for new betting round (table handles this)
	entity.currentBet = 0
	entity.potManager.ResetCurrentBets()

	// Update phase to FLOP
	entity.phase = pokerrpc.GamePhase_FLOP

	if callback != nil {
		callback("FLOP", statemachine.StateEntered)
	}

	// Check if betting should advance immediately to next phase
	switch entity.betRound {
	case 1: // Flop betting complete, move to turn
		entity.betRound++
		return stateTurn
	default:
		// Stay in flop for betting
		return stateFlop
	}
}

// stateTurn deals the turn (fourth community card)
func stateTurn(entity *Game, callback func(stateName string, event statemachine.StateEvent)) GameStateFn {
	// Deal the turn (4th community card)
	card, ok := entity.deck.Draw()
	if !ok {
		if callback != nil {
			callback("END", statemachine.StateEntered)
		}
		return stateEnd // End game if deck is empty
	}
	entity.communityCards = append(entity.communityCards, card)

	// Reset bets for new betting round (table handles this)
	entity.currentBet = 0
	entity.potManager.ResetCurrentBets()

	// Update phase to TURN
	entity.phase = pokerrpc.GamePhase_TURN

	if callback != nil {
		callback("TURN", statemachine.StateEntered)
	}

	// Check if betting should advance immediately to next phase
	switch entity.betRound {
	case 2: // Turn betting complete, move to river
		entity.betRound++
		return stateRiver
	default:
		// Stay in turn for betting
		return stateTurn
	}
}

// stateRiver deals the river (fifth community card)
func stateRiver(entity *Game, callback func(stateName string, event statemachine.StateEvent)) GameStateFn {
	// Deal the river (5th community card)
	card, ok := entity.deck.Draw()
	if !ok {
		if callback != nil {
			callback("END", statemachine.StateEntered)
		}
		return stateEnd // End game if deck is empty
	}
	entity.communityCards = append(entity.communityCards, card)

	// Reset bets for new betting round (table handles this)
	entity.currentBet = 0
	entity.potManager.ResetCurrentBets()

	// Update phase to RIVER
	entity.phase = pokerrpc.GamePhase_RIVER

	if callback != nil {
		callback("RIVER", statemachine.StateEntered)
	}

	// Check if betting should advance immediately to showdown
	switch entity.betRound {
	case 3: // River betting complete, move to showdown
		return stateShowdown
	default:
		// Stay in river for betting
		return stateRiver
	}
}

// stateShowdown determines the winner of the hand
func stateShowdown(entity *Game, callback func(stateName string, event statemachine.StateEvent)) GameStateFn {
	// Update phase to SHOWDOWN
	entity.phase = pokerrpc.GamePhase_SHOWDOWN

	// Count active (non-folded) players
	activePlayers := make([]*Player, 0)
	for _, player := range entity.players {
		if !player.HasFolded {
			activePlayers = append(activePlayers, player)
		}
	}

	// Track winners before distributing pots
	entity.winners = make([]string, 0)

	// If only one player remains, they win automatically without hand evaluation
	if len(activePlayers) <= 1 {
		// Award the pot to the remaining player (if any)
		if len(activePlayers) == 1 {
			winnings := entity.GetPot()
			activePlayers[0].Balance += winnings
			entity.winners = append(entity.winners, activePlayers[0].ID)
		}
	} else {
		// Multiple players remain - need proper hand evaluation
		// Only evaluate hands if we have enough cards (player hand + community cards >= 5)
		validEvaluations := true

		for _, player := range activePlayers {
			totalCards := len(player.Hand) + len(entity.communityCards)
			if totalCards < 5 {
				validEvaluations = false
				break
			}
		}

		if validEvaluations {
			// Evaluate each active player's hand
			for _, player := range activePlayers {
				handValue := EvaluateHand(player.Hand, entity.communityCards)
				player.HandValue = &handValue
				player.HandDescription = GetHandDescription(handValue)
			}

			// Check for any uncalled bets and return them
			entity.potManager.ReturnUncalledBet(entity.players)

			// Create side pots if needed
			entity.potManager.CreateSidePots(entity.players)

			// Distribute pots to winners
			entity.potManager.DistributePots(entity.players)

			// Track all winners who won money
			for _, player := range activePlayers {
				// Check if player received winnings by comparing balance before and after
				if player.Balance > 0 {
					entity.winners = append(entity.winners, player.ID)
				}
			}
		} else {
			// Can't properly evaluate hands - award pot to first active player
			// This is a fallback for incomplete games
			if len(activePlayers) > 0 {
				winnings := entity.GetPot()
				activePlayers[0].Balance += winnings
				entity.winners = append(entity.winners, activePlayers[0].ID)
			}
		}
	}

	if callback != nil {
		callback("SHOWDOWN", statemachine.StateEntered)
	}

	// Return to pre-deal to start a new hand
	return statePreDeal
}

// stateEnd terminates the game
func stateEnd(entity *Game, callback func(stateName string, event statemachine.StateEvent)) GameStateFn {
	if callback != nil {
		callback("END", statemachine.StateEntered)
	}
	return nil // Return nil to terminate the state machine
}

// GetPot returns the total pot amount
func (g *Game) GetPot() int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return g.potManager.GetTotalPot()
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

// GetWinners returns the winners of the game
func (g *Game) GetWinners() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.winners
}

// SetPlayers sets the players for this game from table users
func (g *Game) SetPlayers(users []*User) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Convert users to players for game management using proper constructor
	g.players = make([]*Player, len(users))
	for i, user := range users {
		// Create player using constructor to ensure state machine is initialized
		player := NewPlayer(user.ID, user.Name, g.config.StartingChips)

		// Copy table-level state from user
		player.AccountBalance = user.AccountBalance
		player.TableSeat = user.TableSeat
		player.IsReady = user.IsReady
		player.LastAction = user.LastAction

		g.players[i] = player
	}
}

// IncrementActionsInRound increments the action counter for the current betting round
func (g *Game) IncrementActionsInRound() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.actionsInRound++
}

// GetActionsInRound returns the current actions count for this betting round
func (g *Game) GetActionsInRound() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.actionsInRound
}

// ResetActionsInRound resets the action counter for a new betting round
func (g *Game) ResetActionsInRound() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.actionsInRound = 0
}

// ResetForNewHand resets the game state for a new hand while preserving the game instance
func (g *Game) ResetForNewHand(activePlayers []*Player) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Update player references for this hand - use the same objects to maintain unified state
	g.players = activePlayers

	// Reset hand-specific state
	g.communityCards = nil
	g.potManager = NewPotManager()
	g.currentBet = 0
	g.round++
	g.betRound = 0
	g.winners = nil

	// Advance dealer position for new hand
	if len(activePlayers) > 0 {
		g.dealer = (g.dealer + 1) % len(activePlayers)
	}

	// Create new shuffled deck for new hand
	g.deck = NewDeck(g.deck.rng)

	// Set phase to NEW_HAND_DEALING to signal setup in progress
	g.phase = pokerrpc.GamePhase_NEW_HAND_DEALING

	// Reset current player to -1 to force initialization
	g.currentPlayer = -1

	// Reset state machine to NEW_HAND_DEALING
	g.stateMachine.SetState(stateNewHandDealing)
}

// HandlePlayerFold handles a player folding in the game (external API)
func (g *Game) HandlePlayerFold(playerID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.handlePlayerFold(playerID)
}

// handlePlayerFold is the core logic without locking (for internal use)
func (g *Game) handlePlayerFold(playerID string) error {
	player := g.getPlayerByID(playerID)
	if player == nil {
		return fmt.Errorf("player not found in game")
	}

	if g.currentPlayerID() != playerID {
		return fmt.Errorf("not your turn to act")
	}

	player.HasFolded = true
	player.LastAction = time.Now()

	// Update player state using state machine dispatch
	g.updatePlayerState(player)

	g.actionsInRound++
	g.advanceToNextPlayer()

	return nil
}

// HandlePlayerCall handles a player calling in the game (external API)
func (g *Game) HandlePlayerCall(playerID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.handlePlayerCall(playerID)
}

// handlePlayerCall is the core logic without locking (for internal use)
func (g *Game) handlePlayerCall(playerID string) error {
	player := g.getPlayerByID(playerID)
	if player == nil {
		return fmt.Errorf("player not found in game")
	}

	if g.currentPlayerID() != playerID {
		return fmt.Errorf("not your turn to act")
	}

	if g.currentBet <= player.HasBet {
		return fmt.Errorf("nothing to call - use check instead")
	}

	delta := g.currentBet - player.HasBet
	if delta > player.Balance {
		return fmt.Errorf("insufficient balance to call")
	}

	player.Balance -= delta
	player.HasBet = g.currentBet
	player.LastAction = time.Now()

	// Update player state using state machine dispatch
	g.updatePlayerState(player)

	// Find player index and add to pot
	for i, p := range g.players {
		if p.ID == playerID {
			g.AddToPotForPlayer(i, delta)
			break
		}
	}

	g.actionsInRound++
	g.advanceToNextPlayer()

	return nil
}

// HandlePlayerCheck handles a player checking in the game (external API)
func (g *Game) HandlePlayerCheck(playerID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.handlePlayerCheck(playerID)
}

// handlePlayerCheck is the core logic without locking (for internal use)
func (g *Game) handlePlayerCheck(playerID string) error {
	player := g.getPlayerByID(playerID)
	if player == nil {
		return fmt.Errorf("player not found in game")
	}

	if g.currentPlayerID() != playerID {
		return fmt.Errorf("not your turn to act")
	}

	if player.HasBet < g.currentBet {
		return fmt.Errorf("cannot check when there's a bet to call (player bet: %d, current bet: %d)",
			player.HasBet, g.currentBet)
	}

	player.LastAction = time.Now()
	g.actionsInRound++
	g.advanceToNextPlayer()

	return nil
}

// HandlePlayerBet handles a player betting in the game (external API)
func (g *Game) HandlePlayerBet(playerID string, amount int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.handlePlayerBet(playerID, amount)
}

// handlePlayerBet is the core logic without locking (for internal use)
func (g *Game) handlePlayerBet(playerID string, amount int64) error {
	player := g.getPlayerByID(playerID)
	if player == nil {
		return fmt.Errorf("player not found in game")
	}

	if g.currentPlayerID() != playerID {
		return fmt.Errorf("not your turn to act")
	}

	if amount < player.HasBet {
		return fmt.Errorf("cannot decrease bet")
	}

	delta := amount - player.HasBet
	if delta > 0 && delta > player.Balance {
		return fmt.Errorf("insufficient balance")
	}

	if delta > 0 {
		player.Balance -= delta
	}
	player.HasBet = amount
	player.LastAction = time.Now()

	// Update player state using state machine dispatch
	g.updatePlayerState(player)

	if amount > g.currentBet {
		g.currentBet = amount
	}

	// Find player index and add to pot
	if delta > 0 {
		for i, p := range g.players {
			if p.ID == playerID {
				g.AddToPotForPlayer(i, delta)
				break
			}
		}
	}

	g.actionsInRound++
	g.advanceToNextPlayer()

	return nil
}

// updatePlayerState updates a player's state using Rob Pike's pattern - dispatch to let state function decide transitions
func (g *Game) updatePlayerState(player *Player) {
	if player == nil || player.stateMachine == nil {
		return
	}

	// Create callback to handle state transition events
	callback := func(stateName string, event statemachine.StateEvent) {
		// State transitions are handled by the state functions themselves
		// This callback just observes the transitions
	}

	// Dispatch the state machine - the current state function will examine player conditions
	// and return the appropriate next state function based on Rob Pike's pattern
	player.stateMachine.Dispatch(callback)
}

// getPlayerByID finds a player by ID
func (g *Game) getPlayerByID(playerID string) *Player {
	for _, p := range g.players {
		if p.ID == playerID {
			return p
		}
	}
	return nil
}

// currentPlayerID returns the current player's ID
func (g *Game) currentPlayerID() string {
	if g.currentPlayer < 0 || g.currentPlayer >= len(g.players) {
		return ""
	}
	return g.players[g.currentPlayer].ID
}

// advanceToNextPlayer moves to the next active player
func (g *Game) advanceToNextPlayer() {
	if len(g.players) == 0 {
		return
	}

	playersChecked := 0
	maxPlayers := len(g.players)

	for {
		g.currentPlayer = (g.currentPlayer + 1) % len(g.players)
		playersChecked++

		if playersChecked >= maxPlayers {
			break
		}

		if !g.players[g.currentPlayer].HasFolded {
			break
		}
	}
}

// ShowdownResult contains the results of a showdown for table notifications
type ShowdownResult struct {
	Winners    []string
	WinnerInfo []*pokerrpc.Winner
	TotalPot   int64
}

// HandleShowdown processes the showdown logic and returns results (external API)
func (g *Game) HandleShowdown() *ShowdownResult {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.handleShowdown()
}

// handleShowdown is the core logic without locking (for internal use)
func (g *Game) handleShowdown() *ShowdownResult {
	// Count active players (non-folded)
	activePlayers := make([]*Player, 0)
	for _, player := range g.players {
		if !player.HasFolded {
			activePlayers = append(activePlayers, player)
		}
	}

	// Track winners and create result
	result := &ShowdownResult{
		Winners:    make([]string, 0),
		WinnerInfo: make([]*pokerrpc.Winner, 0),
		TotalPot:   g.getPot(),
	}

	// If only one player remains, they win automatically without hand evaluation
	if len(activePlayers) <= 1 {
		if len(activePlayers) == 1 {
			winner := activePlayers[0]
			winnings := g.getPot()
			winner.Balance += winnings
			result.Winners = append(result.Winners, winner.ID)

			// Create winner notification with their cards
			result.WinnerInfo = append(result.WinnerInfo, &pokerrpc.Winner{
				PlayerId: winner.ID,
				Winnings: winnings,
				BestHand: CreateHandFromCards(winner.Hand),
			})
		}
	} else {
		// Multiple players remain - need proper hand evaluation
		validEvaluations := true

		// Check if we have enough cards for evaluation
		for _, player := range activePlayers {
			totalCards := len(player.Hand) + len(g.communityCards)
			if totalCards < 5 {
				validEvaluations = false
				break
			}
		}

		if validEvaluations {
			// Evaluate each active player's hand
			for _, player := range activePlayers {
				handValue := EvaluateHand(player.Hand, g.communityCards)
				player.HandValue = &handValue
				player.HandDescription = GetHandDescription(handValue)
			}

			// Check for any uncalled bets and return them
			g.potManager.ReturnUncalledBet(g.players)

			// Create side pots if needed
			g.potManager.CreateSidePots(g.players)

			// Find the actual winners by comparing hand values
			var bestPlayers []*Player
			var bestHandValue *HandValue

			for _, player := range activePlayers {
				if player.HandValue != nil {
					if bestHandValue == nil || CompareHands(*player.HandValue, *bestHandValue) > 0 {
						bestHandValue = player.HandValue
						bestPlayers = []*Player{player}
					} else if CompareHands(*player.HandValue, *bestHandValue) == 0 {
						bestPlayers = append(bestPlayers, player)
					}
				}
			}

			// Store total pot before distribution
			totalPot := g.getPot()

			// Distribute pots to winners
			g.potManager.DistributePots(g.players)

			// Create winner notifications
			winningsPerPlayer := totalPot / int64(len(bestPlayers))

			for _, winner := range bestPlayers {
				result.Winners = append(result.Winners, winner.ID)

				result.WinnerInfo = append(result.WinnerInfo, &pokerrpc.Winner{
					PlayerId: winner.ID,
					HandRank: winner.HandValue.HandRank,
					BestHand: CreateHandFromCards(winner.HandValue.BestHand),
					Winnings: winningsPerPlayer,
				})
			}
		} else {
			// Can't properly evaluate hands - award pot to first active player
			if len(activePlayers) > 0 {
				winner := activePlayers[0]
				winnings := g.getPot()
				winner.Balance += winnings
				result.Winners = append(result.Winners, winner.ID)

				result.WinnerInfo = append(result.WinnerInfo, &pokerrpc.Winner{
					PlayerId: winner.ID,
					Winnings: winnings,
					BestHand: CreateHandFromCards(winner.Hand),
				})
			}
		}
	}

	// Set phase to showdown
	g.phase = pokerrpc.GamePhase_SHOWDOWN
	g.winners = result.Winners

	return result
}

// SyncBalancesToUsers updates the user balances based on game player balances (external API)
func (g *Game) SyncBalancesToUsers(users map[string]*User) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	g.syncBalancesToUsers(users)
}

// syncBalancesToUsers is the core logic without locking (for internal use)
func (g *Game) syncBalancesToUsers(users map[string]*User) {
	for _, player := range g.players {
		if user, exists := users[player.ID]; exists {
			user.AccountBalance = player.Balance
		}
	}
}

// getPot is the core logic without locking (for internal use)
func (g *Game) getPot() int64 {
	return g.potManager.GetTotalPot()
}

// MaybeAdvancePhase checks if betting round is finished and progresses the game phase (external API)
func (g *Game) MaybeAdvancePhase() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.maybeAdvancePhase()
}

// maybeAdvancePhase is the core logic without locking (for internal use)
func (g *Game) maybeAdvancePhase() {
	// Don't advance during NEW_HAND_DEALING phase - this is managed by setupNewHandLocked()
	// which handles the complete setup sequence and phase transitions internally
	if g.phase == pokerrpc.GamePhase_NEW_HAND_DEALING {
		return
	}

	// Count active players (non-folded) from game players
	activePlayers := 0
	for _, p := range g.players {
		if !p.HasFolded {
			activePlayers++
		}
	}

	// If only one player remains, advance to showdown
	if activePlayers <= 1 {
		g.phase = pokerrpc.GamePhase_SHOWDOWN
		g.stateMachine.SetState(stateShowdown)
		return
	}

	// Check if all active players have had a chance to act and all bets are equal
	// A betting round is complete when:
	// 1. At least each active player has had one action (actionsInRound >= activePlayers)
	// 2. All active players have matching bets (or have folded)

	if g.actionsInRound < activePlayers {
		return // Not all players have acted yet
	}

	// Check if all active players have matching bets
	unmatchedPlayers := 0
	for _, p := range g.players {
		if p.HasFolded {
			continue
		}
		if p.HasBet != g.currentBet {
			unmatchedPlayers++
		}
	}

	if unmatchedPlayers > 0 {
		return // Still players with unmatched bets
	}

	// Betting round is complete - advance to next phase
	switch g.phase {
	case pokerrpc.GamePhase_PRE_FLOP:
		g.StateFlop()
		g.stateMachine.SetState(stateFlop)
	case pokerrpc.GamePhase_FLOP:
		g.StateTurn()
		g.stateMachine.SetState(stateTurn)
	case pokerrpc.GamePhase_TURN:
		g.StateRiver()
		g.stateMachine.SetState(stateRiver)
	case pokerrpc.GamePhase_RIVER:
		g.phase = pokerrpc.GamePhase_SHOWDOWN
		g.stateMachine.SetState(stateShowdown)
		return
	}

	// Reset for new betting round
	for _, p := range g.players {
		p.HasBet = 0
	}
	g.currentBet = 0
	g.ResetActionsInRound() // Reset actions counter for new betting round

	// Reset current player for new betting round
	g.initializeCurrentPlayer()

	// Set the new current player's LastAction to now for the new betting round
	if g.currentPlayer >= 0 && g.currentPlayer < len(g.players) {
		if !g.players[g.currentPlayer].HasFolded {
			g.players[g.currentPlayer].LastAction = time.Now()
		}
	}
}

// AdvanceToNextPlayer moves to the next active player (external API)
func (g *Game) AdvanceToNextPlayer() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.advanceToNextPlayer()
}

// initializeCurrentPlayer sets the current player based on game phase and rules
func (g *Game) initializeCurrentPlayer() {
	if len(g.players) == 0 {
		g.currentPlayer = -1
		return
	}

	numPlayers := len(g.players)

	// In pre-flop, start with Under the Gun (player after big blind)
	if g.phase == pokerrpc.GamePhase_PRE_FLOP {
		if numPlayers == 2 {
			// In heads-up, after blinds are posted, small blind acts first
			// The small blind IS the dealer in heads-up
			g.currentPlayer = g.dealer
		} else {
			// In multi-way, Under the Gun acts first (after big blind)
			g.currentPlayer = (g.dealer + 3) % numPlayers
		}
	} else {
		// In post-flop streets, start with small blind position
		if numPlayers == 2 {
			// In heads-up, small blind is the dealer
			g.currentPlayer = g.dealer
		} else {
			// In multi-way, small blind is player after dealer
			g.currentPlayer = (g.dealer + 1) % numPlayers
		}
	}

	// Ensure we start with an active player and handle edge cases
	playersChecked := 0
	maxPlayers := len(g.players)

	for {
		// Validate currentPlayer is within bounds
		if g.currentPlayer < 0 || g.currentPlayer >= len(g.players) {
			g.currentPlayer = 0 // Reset to first player if out of bounds
		}

		// Use the unified player state directly
		if !g.players[g.currentPlayer].HasFolded {
			break
		}

		g.currentPlayer = (g.currentPlayer + 1) % len(g.players)
		playersChecked++

		// Prevent infinite loop by checking all players at most once
		if playersChecked >= maxPlayers {
			// All players have folded - this shouldn't happen during initialization
			// Default to first player
			g.currentPlayer = 0
			break
		}
	}
}

// GetRound returns the current round number
func (g *Game) GetRound() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.round
}

// GetBetRound returns the current betting round
func (g *Game) GetBetRound() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.betRound
}

// GetDealer returns the dealer position
func (g *Game) GetDealer() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.dealer
}

// GetDeckState returns the current deck state for persistence
func (g *Game) GetDeckState() interface{} {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.deck == nil {
		return nil
	}
	// Return the remaining cards in the deck
	return g.deck.cards
}

// SetGameState allows restoring game state from persistence
func (g *Game) SetGameState(dealer, currentPlayer, round, betRound int, currentBet, pot int64, phase pokerrpc.GamePhase) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.dealer = dealer
	g.currentPlayer = currentPlayer
	g.round = round
	g.betRound = betRound
	g.currentBet = currentBet
	g.phase = phase
	// Note: Pot will be restored through the PotManager when restoring player bets
	// We can't directly set the pot value, but it will be calculated from player bets
}

// SetCommunityCards allows restoring community cards from persistence
func (g *Game) SetCommunityCards(cards []Card) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.communityCards = make([]Card, len(cards))
	copy(g.communityCards, cards)
}

package poker

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/decred/slog"

	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/pokerbisonrelay/pkg/statemachine"
)

// GameStateFn represents a game state function following Rob Pike's pattern
type GameStateFn = statemachine.StateFn[Game]

// GameConfig holds configuration for a new game
type GameConfig struct {
	NumPlayers     int
	StartingChips  int64         // Fixed number of chips each player starts with
	SmallBlind     int64         // Small blind amount
	BigBlind       int64         // Big blind amount
	Seed           int64         // Optional seed for deterministic games
	AutoStartDelay time.Duration // Delay before automatically starting next hand after showdown
	TimeBank       time.Duration // Time bank for each player
	Log            slog.Logger   // Logger for game events
}

// AutoStartCallbacks defines the callback functions needed for auto-start functionality
type AutoStartCallbacks struct {
	MinPlayers func() int
	// StartNewHand should start a new hand
	StartNewHand func() error
	// OnNewHandStarted is called after a new hand has been successfully started
	OnNewHandStarted func()
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

	// Auto-start management
	autoStartTimer     *time.Timer
	autoStartCanceled  bool
	autoStartCallbacks *AutoStartCallbacks

	// Logger
	log slog.Logger

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
func NewGame(cfg GameConfig) (*Game, error) {
	if cfg.NumPlayers < 2 {
		panic("poker: must have at least 2 players")
	}

	if cfg.Log == nil {
		return nil, fmt.Errorf("poker: log is required")
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
		potManager:      NewPotManager(cfg.NumPlayers),
		currentBet:      0,
		round:           0,
		betRound:        0,
		config:          cfg,
		log:             cfg.Log,
		errorSimulation: false,
		phase:           pokerrpc.GamePhase_NEW_HAND_DEALING,
	}

	// Initialize state machine with first state function
	g.stateMachine = statemachine.NewStateMachine(g, stateNewHandDealing)

	return g, nil
}

// State functions following Rob Pike's pattern
// Each state function performs its work and returns the next state function (or nil to terminate)

// stateNewHandDealing handles the NEW_HAND_DEALING phase
func stateNewHandDealing(entity *Game) GameStateFn {
	// This state is primarily managed by the table layer
	// The table handles card dealing and blind posting, then transitions to PRE_FLOP
	// This state function is mainly for completeness in the state machine
	entity.phase = pokerrpc.GamePhase_NEW_HAND_DEALING
	return statePreDeal
}

// statePreDeal prepares the game for a new hand
func statePreDeal(entity *Game) GameStateFn {
	// Reset game state for a new hand
	entity.round++

	// Reset the deck, community cards, pot, etc.
	entity.deck.Shuffle()
	entity.communityCards = []Card{}
	entity.currentBet = 0
	entity.betRound = 0

	// Rotate dealer position
	entity.dealer = (entity.dealer + 1) % len(entity.players)
	// Don't set currentPlayer here - it will be set correctly in stateBlinds

	// Set phase to PRE_FLOP (game about to start)
	entity.phase = pokerrpc.GamePhase_PRE_FLOP

	return stateDeal
}

// stateDeal deals initial cards to players
func stateDeal(entity *Game) GameStateFn {
	// Note: Card dealing is handled by the table layer to maintain
	// consistency with existing game flow. This state is mainly for
	// state machine progression.

	// After dealing (handled externally), move to blinds state
	return stateBlinds
}

// stateBlinds handles posting small and big blinds and sets the current player
func stateBlinds(entity *Game) GameStateFn {
	numPlayers := len(entity.players)
	if numPlayers < 2 {
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

	// Helper that posts a blind only if it hasn't already been posted for the hand.
	postBlind := func(pos int, amount int64) {
		p := entity.players[pos]
		if p == nil {
			return
		}
		// Skip if this player already has an equal or greater bet recorded (blind already posted).
		if p.HasBet >= amount {
			return
		}
		if amount > p.Balance {
			// Player cannot cover blind – treat as all-in of remaining balance.
			amount = p.Balance
			p.stateMachine.Dispatch(playerStateAllIn)
		}
		p.Balance -= amount
		p.HasBet += amount
		entity.potManager.AddBet(pos, amount, entity.players)
	}

	// Post blinds, guarding against duplicates.
	postBlind(smallBlindPos, entity.config.SmallBlind)
	postBlind(bigBlindPos, entity.config.BigBlind)

	// Set first player to act (after big blind for pre-flop)
	if numPlayers == 2 {
		// In heads-up, small blind acts first pre-flop
		entity.currentPlayer = smallBlindPos
	} else {
		// With 3+ players, first to act is after big blind
		entity.currentPlayer = (bigBlindPos + 1) % numPlayers
	}

	// Move to pre-flop betting
	return statePreFlop
}

// statePreFlop handles the pre-flop betting round logic
func statePreFlop(entity *Game) GameStateFn {
	// This is a betting round - handled by external logic
	// Based on betting completion, determine next state

	switch entity.betRound {
	case 0: // Pre-flop complete, move to flop
		entity.betRound++
		return stateFlop
	default:
		// Still in pre-flop betting - stay in this state
		return statePreFlop
	}
}

// stateFlop handles the flop betting round (community cards dealt by StateFlop method)
func stateFlop(entity *Game) GameStateFn {
	// Community cards are dealt by the StateFlop() method, not here
	// This state function just handles the betting round logic

	// Reset bets for new betting round (table handles this)
	entity.currentBet = 0
	entity.potManager.ResetCurrentBets()

	// Update phase to FLOP
	entity.phase = pokerrpc.GamePhase_FLOP

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

// stateTurn handles the turn betting round (community cards dealt by StateTurn method)
func stateTurn(entity *Game) GameStateFn {
	// Community cards are dealt by the StateTurn() method, not here
	// This state function just handles the betting round logic

	// Reset bets for new betting round (table handles this)
	entity.currentBet = 0
	entity.potManager.ResetCurrentBets()

	// Update phase to TURN
	entity.phase = pokerrpc.GamePhase_TURN

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

// stateRiver handles the river betting round (community cards dealt by StateRiver method)
func stateRiver(entity *Game) GameStateFn {
	// Community cards are dealt by the StateRiver() method, not here
	// This state function just handles the betting round logic

	// Reset bets for new betting round (table handles this)
	entity.currentBet = 0
	entity.potManager.ResetCurrentBets()

	// Update phase to RIVER
	entity.phase = pokerrpc.GamePhase_RIVER

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
func stateShowdown(entity *Game) GameStateFn {
	// Mark phase as SHOWDOWN; actual showdown resolution is handled by Table.handleShowdown → Game.handleShowdown
	entity.log.Debugf("stateShowdown: entered showdown state")
	entity.phase = pokerrpc.GamePhase_SHOWDOWN

	// Remain in SHOWDOWN state until the Table schedules the next hand.
	return stateShowdown
}

// stateEnd terminates the game
func stateEnd(entity *Game) GameStateFn {
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

	// Guard: only deal flop if we haven't already dealt it
	if len(g.communityCards) >= 3 {
		g.phase = pokerrpc.GamePhase_FLOP
		return
	}

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

	// Guard: only deal turn if we haven't already dealt it
	if len(g.communityCards) >= 4 {
		g.phase = pokerrpc.GamePhase_TURN
		return
	}

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

	// Guard: only deal river if we haven't already dealt it
	if len(g.communityCards) >= 5 {
		g.phase = pokerrpc.GamePhase_RIVER
		return
	}

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

	g.potManager.AddBet(playerIndex, amount, g.players)
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
		player.TableSeat = user.TableSeat
		player.IsReady = user.IsReady
		player.LastAction = time.Now() // Set current time since User doesn't have LastAction

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
func (g *Game) ResetForNewHand(activePlayers []*Player) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Update player references for this hand - use the same objects to maintain unified state
	g.players = activePlayers
	potManager := NewPotManager(len(activePlayers))

	// Reset hand-specific state
	g.communityCards = nil
	g.potManager = potManager // Reset pot manager for new hand
	g.currentBet = 0
	g.round++
	g.betRound = 0
	g.winners = nil

	// Advance dealer position for new hand
	if len(activePlayers) > 0 {
		g.dealer = (g.dealer + 1) % len(activePlayers)
	}

	// Create a shuffled deck for the new hand.
	// If a deterministic seed is configured, advance the sequence by incorporating
	// the round to avoid identical decks each hand.
	var nextRng *rand.Rand
	if g.config.Seed != 0 {
		// Derive a unique seed per hand deterministically
		derived := g.config.Seed + int64(g.round)
		nextRng = rand.New(rand.NewSource(derived))
	} else {
		// For non-deterministic games, ensure each hand gets a fresh RNG seed so
		// rapid successive hands don't accidentally reuse identical shuffles.
		base := time.Now().UnixNano()
		var mix int64 = 0
		if g.deck != nil && g.deck.rng != nil {
			mix = g.deck.rng.Int63()
		}
		seed := base ^ mix ^ int64(g.round)
		nextRng = rand.New(rand.NewSource(seed))
	}
	g.deck = NewDeck(nextRng)

	// Set phase to NEW_HAND_DEALING to signal setup in progress
	g.phase = pokerrpc.GamePhase_NEW_HAND_DEALING

	// Reset current player to -1 to force initialization
	g.currentPlayer = -1

	// Reset state machine to NEW_HAND_DEALING
	g.stateMachine.Dispatch(stateNewHandDealing)

	return nil
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

	player.stateMachine.Dispatch(playerStateFolded)
	player.LastAction = time.Now()
	g.updatePlayerState(player)
	g.actionsInRound++

	// Count alive
	active := 0
	for _, p := range g.players {
		if p != nil && p.GetCurrentStateString() != "FOLDED" {
			active++
		}
	}

	// If only one remains, finish NOW via state machine
	if active == 1 {
		g.phase = pokerrpc.GamePhase_SHOWDOWN
		g.stateMachine.Dispatch(stateShowdown)
		g.log.Debugf("handlePlayerFold: only %d active players, moving to SHOWDOWN", active)

		// Do NOT advance turn after hand is finished
		return nil
	}

	// Otherwise continue normal action
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
		// Player cannot afford to call - make them all-in with remaining balance
		g.log.Debugf("Player %s cannot afford to call %d (has %d), going all-in", player.ID, delta, player.Balance)
		delta = player.Balance
		player.stateMachine.Dispatch(playerStateAllIn)
		player.LastAction = time.Now()

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
		// Player cannot afford the bet - make them all-in with remaining balance
		g.log.Debugf("Player %s cannot afford to bet %d (has %d), going all-in", player.ID, delta, player.Balance)
		delta = player.Balance
		amount = player.HasBet + delta
		player.stateMachine.Dispatch(playerStateAllIn)
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

	// Dispatch the state machine - the current state function will examine player conditions
	// and return the appropriate next state function based on Rob Pike's pattern
	player.stateMachine.Dispatch(player.stateMachine.GetCurrentState())
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

		// Skip folded players and all-in players (they can't act)
		if g.players[g.currentPlayer].GetCurrentStateString() != "FOLDED" && g.players[g.currentPlayer].GetCurrentStateString() != "ALL_IN" {
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
func (g *Game) HandleShowdown() (*ShowdownResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.handleShowdown()
}

// handleShowdown is the core logic without locking (for internal use)
func (g *Game) handleShowdown() (*ShowdownResult, error) {
	g.log.Debugf("handleShowdown: entered showdown processing")

	// Gather active (non-folded) players
	activePlayers := make([]*Player, 0, len(g.players))
	for _, player := range g.players {
		if player != nil && player.GetCurrentStateString() != "FOLDED" {
			activePlayers = append(activePlayers, player)
		}
	}

	// Prepare result
	result := &ShowdownResult{
		Winners:    make([]string, 0),
		WinnerInfo: make([]*pokerrpc.Winner, 0),
		TotalPot:   0, // Will be set after pot rebuilding
	}

	// --- Uncontested (fold-win): build pots, award total, reset state
	if len(activePlayers) == 1 {
		winner := activePlayers[0]
		g.log.Infof("HERE ON ONE ACTIVE PLAYER: %s", winner.ID)

		sum := int64(0)
		for _, p := range g.potManager.Pots {
			sum += p.Amount
		}

		// Total pot for the event
		result.TotalPot = g.potManager.GetTotalPot()

		// --- Use delta accounting to populate result (avoids “empty winners”)
		prev := make(map[string]int64, len(g.players))
		for _, p := range g.players {
			if p != nil {
				prev[p.ID] = p.Balance
			}
		}

		g.potManager.DistributePots(g.players)

		// Fill result from actual balance deltas (handles any future edge cases too)
		totalWinnings := int64(0)
		for _, p := range g.players {
			if p == nil {
				continue
			}
			delta := p.Balance - prev[p.ID]
			if delta > 0 {
				result.Winners = append(result.Winners, p.ID)

				// Best hand (use hole cards if board < 5)
				var best []Card
				if len(p.Hand)+len(g.communityCards) >= 5 {
					hv, err := EvaluateHand(p.Hand, g.communityCards)
					if err != nil {
						return nil, fmt.Errorf("failed to evaluate hand for player %s: %w", p.ID, err)
					}
					p.HandValue = &hv
					p.HandDescription = GetHandDescription(hv)
					best = hv.BestHand
				} else {
					best = p.Hand
				}

				result.WinnerInfo = append(result.WinnerInfo, &pokerrpc.Winner{
					PlayerId: p.ID,
					BestHand: CreateHandFromCards(best),
					Winnings: delta,
				})
				totalWinnings += delta
			}
		}

		// Now reset for next hand (and clear unswept for clean logs)

		g.phase = pokerrpc.GamePhase_SHOWDOWN
		g.winners = result.Winners
		g.log.Infof("result: %+v", result)
		return result, nil
	}

	// --- True showdown: require enough cards to evaluate
	for _, p := range activePlayers {
		if len(p.Hand)+len(g.communityCards) < 5 {
			msg := fmt.Sprintf("invalid showdown: player %s has insufficient cards (hole=%d, board=%d)",
				p.ID, len(p.Hand), len(g.communityCards))
			g.log.Errorf(msg)
			panic(msg)
		}
	}

	// Evaluate each active player's hand
	for _, p := range activePlayers {
		hv, err := EvaluateHand(p.Hand, g.communityCards)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate hand for player %s: %w", p.ID, err)
		}
		p.HandValue = &hv
		p.HandDescription = GetHandDescription(hv)
		g.log.Debugf("handleShowdown: player %s hand=%v description=%s", p.ID, p.Hand, p.HandDescription)
	}

	// Set TotalPot after rebuilding pots
	result.TotalPot = g.potManager.GetTotalPot()

	g.log.Debugf("handleShowdown: total pots=%d", len(g.potManager.Pots))
	for i, pot := range g.potManager.Pots {
		g.log.Debugf("handleShowdown: pot %d amount=%d eligible_players=%v", i, pot.Amount, pot.Eligibility)
	}

	// Snapshot balances to compute exact deltas
	prev := make(map[string]int64, len(g.players))
	for _, p := range g.players {
		if p != nil {
			prev[p.ID] = p.Balance
			g.log.Debugf("handleShowdown: player %s balance before distribution=%d", p.ID, p.Balance)
		}
	}

	// Distribute pots
	if err := g.potManager.DistributePots(g.players); err != nil {
		g.log.Errorf("Failed to distribute pots: %v", err)
		return nil, err
	}

	// Collect winners by positive delta
	for _, p := range g.players {
		if p == nil {
			continue
		}
		delta := p.Balance - prev[p.ID]
		g.log.Debugf("handleShowdown: player %s balance after distribution=%d delta=%d", p.ID, p.Balance, delta)
		if delta > 0 {
			result.Winners = append(result.Winners, p.ID)
			var handRank pokerrpc.HandRank
			var best []Card
			if p.HandValue != nil {
				handRank = p.HandValue.HandRank
				best = p.HandValue.BestHand
			} else {
				best = p.Hand
			}
			result.WinnerInfo = append(result.WinnerInfo, &pokerrpc.Winner{
				PlayerId: p.ID,
				HandRank: handRank,
				BestHand: CreateHandFromCards(best),
				Winnings: delta,
			})
		}
	}

	// Assertion helper: log pot sums to catch regressions
	totalWinnings := int64(0)
	for _, winner := range result.WinnerInfo {
		totalWinnings += winner.Winnings
	}

	// Mark phase and cache winners
	g.phase = pokerrpc.GamePhase_SHOWDOWN
	g.winners = result.Winners

	return result, nil
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

	// Diagnostic: log entry state
	g.log.Debugf("maybeAdvancePhase: phase=%v actionsInRound=%d currentBet=%d",
		g.phase, g.actionsInRound, g.currentBet)

	// Count active players (non-folded and non-all-in) from game players
	// All-in players can't act, so they don't count toward the action requirement
	activePlayers := 0
	for _, p := range g.players {
		if p.GetCurrentStateString() != "FOLDED" && p.GetCurrentStateString() != "ALL_IN" {
			activePlayers++
		}
	}

	// If only one player remains, advance to showdown
	if activePlayers <= 1 {
		g.phase = pokerrpc.GamePhase_SHOWDOWN
		g.stateMachine.Dispatch(stateShowdown)
		g.log.Debugf("maybeAdvancePhase: only %d active players, moving to SHOWDOWN", activePlayers)
		return
	}

	// Check if all active players have had a chance to act and all bets are equal
	// A betting round is complete when:
	// 1. At least each active player has had one action (actionsInRound >= activePlayers)
	// 2. All active players have matching bets (or have folded)

	if g.actionsInRound < activePlayers {
		g.log.Debugf("maybeAdvancePhase: waiting for actions: %d/%d", g.actionsInRound, activePlayers)
		return // Not all players have acted yet
	}

	// Check if all active players have matching bets
	// All-in players are considered "matched" even if their bet is less than currentBet
	unmatchedPlayers := 0
	for _, p := range g.players {
		if p.GetCurrentStateString() == "FOLDED" {
			continue
		}
		// All-in players are considered matched regardless of their bet amount
		if p.GetCurrentStateString() == "ALL_IN" {
			continue
		}
		if p.HasBet != g.currentBet {
			unmatchedPlayers++
		}
	}

	if unmatchedPlayers > 0 {
		g.log.Debugf("maybeAdvancePhase: %d players have unmatched bets (currentBet=%d)", unmatchedPlayers, g.currentBet)
		return // Still players with unmatched bets
	}

	// Betting round is complete - advance to next phase
	switch g.phase {
	case pokerrpc.GamePhase_PRE_FLOP:
		g.StateFlop()
		g.stateMachine.Dispatch(stateFlop)
		g.log.Debug("maybeAdvancePhase: advanced to FLOP")
	case pokerrpc.GamePhase_FLOP:
		g.StateTurn()
		g.stateMachine.Dispatch(stateTurn)
		g.log.Debug("maybeAdvancePhase: advanced to TURN")
	case pokerrpc.GamePhase_TURN:
		g.StateRiver()
		g.stateMachine.Dispatch(stateRiver)
		g.log.Debug("maybeAdvancePhase: advanced to RIVER")
	case pokerrpc.GamePhase_RIVER:
		g.phase = pokerrpc.GamePhase_SHOWDOWN
		g.stateMachine.Dispatch(stateShowdown)
		g.log.Debug("maybeAdvancePhase: advanced to SHOWDOWN")
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
	if g.currentPlayer >= 0 && g.currentPlayer < len(g.players) {
		g.log.Debug("maybeAdvancePhase: new round currentPlayer=%d id=%s",
			g.currentPlayer, g.players[g.currentPlayer].ID)
	}

	// Set the new current player's LastAction to now for the new betting round
	if g.currentPlayer >= 0 && g.currentPlayer < len(g.players) {
		if g.players[g.currentPlayer].GetCurrentStateString() != "FOLDED" {
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
		if g.players[g.currentPlayer].GetCurrentStateString() != "FOLDED" {
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

// SetAutoStartCallbacks sets the callback functions for auto-start functionality
func (g *Game) SetAutoStartCallbacks(callbacks *AutoStartCallbacks) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.autoStartCallbacks = callbacks
}

// scheduleAutoStart schedules automatic start of next hand after configured delay
func (g *Game) ScheduleAutoStart() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.scheduleAutoStart()
}

// scheduleAutoStart is the internal implementation
func (g *Game) scheduleAutoStart() {
	// Cancel any existing auto-start timer
	g.cancelAutoStart()

	// Check if auto-start is configured
	if g.config.AutoStartDelay <= 0 || g.autoStartCallbacks == nil {
		g.log.Debugf("scheduleAutoStart: invalid config, delay=%v, callbacks=%v", g.config.AutoStartDelay, g.autoStartCallbacks != nil)
		return
	}

	// Debug log
	g.log.Debugf("scheduleAutoStart: setting up timer with delay %v", g.config.AutoStartDelay)

	// Mark that auto-start is pending
	g.autoStartCanceled = false

	// Schedule the auto-start
	g.autoStartTimer = time.AfterFunc(g.config.AutoStartDelay, func() {
		// Check if auto-start was canceled (without holding lock)
		g.mu.Lock()
		canceled := g.autoStartCanceled
		callbacks := g.autoStartCallbacks
		log := g.log
		g.mu.Unlock()

		if canceled {
			return
		}

		if callbacks == nil {
			return
		}

		readyCount := 0
		for _, player := range g.players {
			// Count players who have sufficient balance (folded status will be reset for new hand)
			if player.Balance >= g.config.BigBlind {
				readyCount++
				log.Debugf("Player %s ready for auto-start: balance=%d >= bigBlind=%d", player.ID, player.Balance, g.config.BigBlind)
			} else {
				log.Debugf("Player %s not ready for auto-start: balance=%d < bigBlind=%d", player.ID, player.Balance, g.config.BigBlind)
			}
		}

		minRequired := callbacks.MinPlayers()
		log.Debugf("Auto-start check: readyCount=%d, minRequired=%d", readyCount, minRequired)
		if readyCount >= minRequired {
			err := callbacks.StartNewHand()
			if err != nil {
				log.Debugf("Auto-start new hand failed: %v", err)
			} else {
				if callbacks.OnNewHandStarted != nil {
					// Invoke the callback
					go callbacks.OnNewHandStarted()
				}
			}
		} else {
			log.Debugf("Not enough players for auto-start: %d < %d", readyCount, minRequired)
		}
	})
}

// CancelAutoStart cancels any pending auto-start timer
func (g *Game) CancelAutoStart() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cancelAutoStart()
}

// cancelAutoStart is the internal implementation (assumes lock is held)
func (g *Game) cancelAutoStart() {
	if g.autoStartTimer != nil {
		g.autoStartTimer.Stop()
		g.autoStartTimer = nil
	}
	g.autoStartCanceled = true
}

// GameStateSnapshot represents a point-in-time snapshot of game state for safe concurrent access
type GameStateSnapshot struct {
	Dealer         int
	CurrentPlayer  int
	CurrentBet     int64
	Pot            int64
	Round          int
	BetRound       int
	CommunityCards []Card
	DeckState      interface{}
	Players        []*Player
}

// GetStateSnapshot returns an atomic snapshot of the game state for safe concurrent access
func (g *Game) GetStateSnapshot() GameStateSnapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// Create a deep copy of players to avoid race conditions
	playersCopy := make([]*Player, len(g.players))
	for i, player := range g.players {
		// Create a copy of the player to avoid race conditions
		playerCopy := &Player{
			ID:              player.ID,
			Name:            player.Name,
			TableSeat:       player.TableSeat,
			IsReady:         player.IsReady,
			Balance:         player.Balance,
			StartingBalance: player.StartingBalance,
			HasBet:          player.HasBet,
			IsDealer:        player.IsDealer,
			IsTurn:          player.IsTurn,
			Hand:            make([]Card, len(player.Hand)),
			HandDescription: player.HandDescription,
			HandValue:       player.HandValue,
			LastAction:      player.LastAction,
		}
		// Copy the hand cards
		copy(playerCopy.Hand, player.Hand)
		playersCopy[i] = playerCopy
	}

	// Copy community cards
	communityCardsCopy := make([]Card, len(g.communityCards))
	copy(communityCardsCopy, g.communityCards)

	// Calculate pot amount based on game phase
	var potAmount int64
	// During showdown, use GetTotalPot() after pots have been built
	potAmount = g.potManager.GetTotalPot()

	return GameStateSnapshot{
		Dealer:         g.dealer,
		CurrentPlayer:  g.currentPlayer,
		CurrentBet:     g.currentBet,
		Pot:            potAmount,
		Round:          g.round,
		BetRound:       g.betRound,
		CommunityCards: communityCardsCopy,
		DeckState:      g.deck.GetState(),
		Players:        playersCopy,
	}
}

// ModifyPlayers executes the provided function while holding the game's write
// lock, giving callers safe, exclusive access to the underlying slice of
// players. This is useful for code that needs to mutate player state outside
// of the poker package (for example, when restoring snapshots) while still
// guaranteeing there are no data races with concurrent reads performed via
// GetStateSnapshot.
func (g *Game) ModifyPlayers(fn func(players []*Player)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	fn(g.players)
}

// ForceSetPot sets the amount of the main pot directly. This is intended to
// be used only during server-side restoration when rebuilding a game from a
// persisted snapshot where the individual betting history is not available.
func (g *Game) ForceSetPot(amount int64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.potManager == nil {
		g.potManager = NewPotManager(len(g.players))
	}

	// Ensure there is at least a main pot.
	if len(g.potManager.Pots) == 0 {
		g.potManager.Pots = []*Pot{NewPot(0)}
	}

	// Set the amount on the main pot directly.
	g.potManager.Pots[0].Amount = amount
}

// SetOnNewHandStartedCallback registers a callback to be executed each time a
// new hand is successfully auto-started. The callback will be invoked from the
// auto-start timer goroutine, so it MUST be thread-safe and return quickly.
func (g *Game) SetOnNewHandStartedCallback(cb func()) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.autoStartCallbacks == nil {
		g.autoStartCallbacks = &AutoStartCallbacks{}
	}
	g.autoStartCallbacks.OnNewHandStarted = cb
}

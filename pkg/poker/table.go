package poker

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/decred/slog"

	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/pokerbisonrelay/pkg/statemachine"
)

// TableEvent represents a table event with type and payload
type TableEvent struct {
	Type    pokerrpc.NotificationType
	TableID string
	Payload interface{}
}

// TableStateFn represents a table state function following Rob Pike's pattern
type TableStateFn = statemachine.StateFn[Table]

// User represents someone seated at the table (not necessarily playing)
type User struct {
	ID                string
	Name              string
	DCRAccountBalance int64 // DCR account balance (in atoms)
	TableSeat         int   // Seat position at the table
	IsReady           bool  // Ready to start/continue games
	JoinedAt          time.Time
	IsDisconnected    bool // Whether the user is disconnected
}

// NewUser creates a new user
func NewUser(id, name string, dcrAccountBalance int64, seat int) *User {
	return &User{
		ID:                id,
		Name:              name,
		DCRAccountBalance: dcrAccountBalance,
		TableSeat:         seat,
		IsReady:           false,
		JoinedAt:          time.Now(),
	}
}

// TableConfig holds configuration for a new poker table
type TableConfig struct {
	ID             string
	Log            slog.Logger
	GameLog        slog.Logger
	HostID         string
	BuyIn          int64 // DCR amount required to join table (in atoms)
	MinPlayers     int
	MaxPlayers     int
	SmallBlind     int64 // Poker chips amount for small blind
	BigBlind       int64 // Poker chips amount for big blind
	MinBalance     int64 // Minimum DCR account balance required (in atoms)
	StartingChips  int64 // Poker chips each player starts with in the game
	TimeBank       time.Duration
	AutoStartDelay time.Duration // Delay before automatically starting next hand after showdown
}

// TableEventManager handles notifications and state updates for table events
type TableEventManager struct {
	eventChannel chan<- TableEvent
}

// SetEventChannel sets the event channel for the event manager
func (tem *TableEventManager) SetEventChannel(eventChannel chan<- TableEvent) {
	tem.eventChannel = eventChannel
}

// PublishEvent publishes an event to the channel (non-blocking)
func (tem *TableEventManager) PublishEvent(eventType pokerrpc.NotificationType, tableID string, payload interface{}) {
	if tem.eventChannel != nil {
		select {
		case tem.eventChannel <- TableEvent{
			Type:    eventType,
			TableID: tableID,
			Payload: payload,
		}:
		default:
			// Channel is full or closed, event is dropped
			// In production, you might want to log this
		}
	}
}

// SetEventChannel sets the event channel for the table
func (t *Table) SetEventChannel(eventChannel chan<- TableEvent) {
	t.eventManager.SetEventChannel(eventChannel)
}

// PublishEvent publishes an event from the table (non-blocking)
func (t *Table) PublishEvent(eventType pokerrpc.NotificationType, tableID string, payload interface{}) {
	t.eventManager.PublishEvent(eventType, tableID, payload)
}

// Table represents a poker table that manages users and delegates game logic to Game
type Table struct {
	log        slog.Logger
	logBackend *logging.LogBackend
	config     TableConfig
	users      map[string]*User // Users seated at the table
	game       *Game            // Game logic that handles all player management
	mu         sync.RWMutex
	createdAt  time.Time
	lastAction time.Time
	// Event manager for notifications
	eventManager *TableEventManager

	// Persist the last showdown result for retrieval after phase advances
	lastShowdown *ShowdownResult

	// Idempotency guard: track which hand (by game round) has been resolved
	resolvedRound int

	// State machine - Rob Pike's pattern
	stateMachine *statemachine.StateMachine[Table]
}

// NewTable creates a new poker table
func NewTable(cfg TableConfig) *Table {
	t := &Table{
		log:          cfg.Log,
		config:       cfg,
		users:        make(map[string]*User),
		createdAt:    time.Now(),
		lastAction:   time.Now(),
		eventManager: &TableEventManager{},
	}

	// Initialize state machine with first state function
	t.stateMachine = statemachine.NewStateMachine(t, tableStateWaitingForPlayers)

	return t
}

// State functions following Rob Pike's pattern
// Each state function performs its work and returns the next state function (or nil to terminate)

// tableStateWaitingForPlayers handles the WAITING_FOR_PLAYERS state logic
func tableStateWaitingForPlayers(entity *Table) TableStateFn {
	// Check if we have enough players and they're all ready
	if len(entity.users) >= entity.config.MinPlayers {
		allReady := true
		for _, u := range entity.users {
			if !u.IsReady {
				allReady = false
				break
			}
		}
		if allReady {
			// Save state when players become ready
			return tableStatePlayersReady
		}
	}

	return tableStateWaitingForPlayers // Stay in this state
}

// tableStatePlayersReady handles the PLAYERS_READY state logic
func tableStatePlayersReady(entity *Table) TableStateFn {
	// Save state when entering players ready
	// This state waits for external trigger (StartGame)
	return tableStatePlayersReady
}

// tableStateGameActive handles the GAME_ACTIVE state logic
func tableStateGameActive(entity *Table) TableStateFn {
	// Save state when game is active
	return tableStateGameActive // Stay in this state during normal gameplay
}

// GetTableStateString returns a string representation of the current table state
func (t *Table) GetTableStateString() string {
	currentState := t.stateMachine.GetCurrentState()
	if currentState == nil {
		return "TERMINATED"
	}

	// Use function pointer comparison to determine state
	switch fmt.Sprintf("%p", currentState) {
	case fmt.Sprintf("%p", tableStateWaitingForPlayers):
		return "WAITING_FOR_PLAYERS"
	case fmt.Sprintf("%p", tableStatePlayersReady):
		return "PLAYERS_READY"
	case fmt.Sprintf("%p", tableStateGameActive):
		return "GAME_ACTIVE"
	default:
		return "UNKNOWN"
	}
}

// CheckAllPlayersReady simplified - just triggers state machine update
func (t *Table) CheckAllPlayersReady() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Let the state machine handle the logic
	t.stateMachine.Dispatch(t.stateMachine.GetCurrentState())

	// Check the resulting state
	state := t.GetTableStateString()
	return state == "PLAYERS_READY" || state == "GAME_ACTIVE"
}

// StartGame starts a new game at the table using the state machine
func (t *Table) StartGame() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check if we're in the right state
	if t.GetTableStateString() != "PLAYERS_READY" {
		return fmt.Errorf("cannot start game: table not in PLAYERS_READY state")
	}

	// shouldn't happen, but just in case
	if t.game != nil {
		t.game = nil
	}

	// Check if we have enough players
	if len(t.users) < t.config.MinPlayers {
		return fmt.Errorf("not enough players to start game")
	}

	// Reset all players for the new hand
	activePlayers := make([]*User, 0, len(t.users))
	for _, u := range t.users {
		activePlayers = append(activePlayers, u)
	}

	// Sort players by TableSeat for consistent ordering
	sort.Slice(activePlayers, func(i, j int) bool {
		return activePlayers[i].TableSeat < activePlayers[j].TableSeat
	})

	var gameLog slog.Logger
	if t.config.GameLog != nil {
		gameLog = t.config.GameLog
	} else {
		gameLog = t.log
	}
	// Create a new game - players are managed by the table
	g, err := NewGame(GameConfig{
		NumPlayers:     len(activePlayers),
		StartingChips:  t.config.StartingChips,
		SmallBlind:     t.config.SmallBlind,
		BigBlind:       t.config.BigBlind,
		AutoStartDelay: t.config.AutoStartDelay,
		Log:            gameLog,
	})
	if err != nil {
		return fmt.Errorf("failed to create game: %w", err)
	}
	t.game = g

	// Set up auto-start callbacks
	t.game.SetAutoStartCallbacks(&AutoStartCallbacks{
		MinPlayers: func() int {
			return t.config.MinPlayers
		},
		StartNewHand: func() error {
			return t.startNewHand()
		},
		OnNewHandStarted: nil, // Server layer will attach this callback if needed
	})

	// Set the players in the game to reference the same objects from the table
	t.game.SetPlayers(activePlayers)

	// Use centralized hand setup logic (this assumes lock is held)
	err = t.setupNewHand(activePlayers)
	if err != nil {
		return err
	}

	// Transition to game active state with broadcast callback
	t.stateMachine.Dispatch(tableStateGameActive)
	t.lastAction = time.Now()
	return nil
}

// IsGameStarted returns whether the game has started
func (t *Table) IsGameStarted() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	state := t.GetTableStateString()
	return state == "GAME_ACTIVE" || state == "SHOWDOWN"
}

// AreAllPlayersReady returns whether all players are ready
func (t *Table) AreAllPlayersReady() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	state := t.GetTableStateString()
	return state == "PLAYERS_READY" || state == "GAME_ACTIVE" || state == "SHOWDOWN"
}

// isGameActive returns true if the game is currently active
func (t *Table) isGameActive() bool {
	state := t.GetTableStateString()
	return state == "GAME_ACTIVE"
}

// handleShowdown delegates showdown logic to the game and handles notifications
func (t *Table) handleShowdown() error {
	if t.game == nil {
		return fmt.Errorf("game is nil")
	}

	currentRound := t.game.GetRound()
	if t.lastShowdown != nil && t.resolvedRound == currentRound {
		// Already resolved winners for this hand - idempotency guard
		return nil
	}

	// Delegate showdown logic to the game and cache authoritative result
	result, err := t.game.handleShowdown()
	if err != nil {
		t.log.Errorf("failed to handle showdown: %v", err)
		return err
	}
	// Persist result for retrieval after phase advances
	t.lastShowdown = result
	t.resolvedRound = currentRound

	tableID := t.config.ID
	amount := t.lastShowdown.TotalPot

	t.PublishEvent(pokerrpc.NotificationType_SHOWDOWN_RESULT, tableID, &pokerrpc.Showdown{
		Winners: t.lastShowdown.WinnerInfo,
		Pot:     amount,
	})

	// Remove busted players (0 chips) and count remaining players
	playersToRemove := make([]string, 0)
	snap := t.game.GetStateSnapshot()
	// Build quick lookup of balances by player ID
	balances := make(map[string]int64, len(snap.Players))
	for _, p := range snap.Players {
		if p != nil {
			balances[p.id] = p.balance
		}
	}

	t.mu.Lock()
	for _, u := range t.users {
		if balances[u.ID] == 0 {
			playersToRemove = append(playersToRemove, u.ID)
		}
	}

	// Check if the game should end BEFORE removing players
	// This ensures all players (including losing ones) get notified
	if t.shouldGameEnd() {
		t.log.Infof("Game should end, calling endGame()")
		t.endGame()
		t.mu.Unlock()
		return nil
	}

	// Remove busted players AFTER game ended notification
	for _, userID := range playersToRemove {
		t.log.Infof("Removing busted player %s (0 chips)", userID)
		t.removeUserWithoutLock(userID)
		t.log.Infof("removed busted player %s (0 chips)", userID)
	}

	// Reset round-local counters and update timestamp
	t.game.ResetActionsInRound()
	t.lastAction = time.Now()

	if t.config.AutoStartDelay == 0 {
		t.log.Debugf("Auto-start delay is 0, skipping auto-start")
	}

	// Schedule auto-start of the next hand strictly after showdown resolution
	if t.config.AutoStartDelay > 0 {
		t.log.Debugf("Scheduling auto-start for new hand with delay %v", t.config.AutoStartDelay)
		// Provide callbacks if not already set (check with game lock)
		if !t.game.HasAutoStartCallbacks() {
			t.game.SetAutoStartCallbacks(&AutoStartCallbacks{
				MinPlayers: func() int {
					remainingPlayers := len(t.users)
					if remainingPlayers >= 2 {
						return 2 // Allow heads-up play
					}
					return t.config.MinPlayers
				},
				StartNewHand:     func() error { return t.startNewHand() },
				OnNewHandStarted: nil,
			})
		}
		t.game.ScheduleAutoStart()
	}
	t.mu.Unlock()
	return nil
}

// shouldGameEnd checks various conditions to determine if the game should end
func (t *Table) shouldGameEnd() bool {
	// Check if we have enough players to continue
	remainingPlayers := len(t.users)
	minRequired := t.config.MinPlayers
	if remainingPlayers >= 2 && remainingPlayers < t.config.MinPlayers {
		minRequired = 2 // Allow heads-up play
	}

	if remainingPlayers < minRequired {
		t.log.Infof("shouldGameEnd: Not enough players remaining (%d < %d)", remainingPlayers, minRequired)
		return true
	}

	// Check if any remaining players have sufficient chips to play
	playersWithChips := 0
	for _, u := range t.users {
		// Find player's current chip balance
		var playerBalance int64 = 0
		for _, player := range t.game.players {
			if player.id == u.ID {
				playerBalance = player.balance
				break
			}
		}

		if playerBalance > 0 {
			playersWithChips++
		}
	}

	if playersWithChips < 2 {
		t.log.Infof("shouldGameEnd: Not enough players with sufficient chips (%d < 2)", playersWithChips)
		return true
	}

	// Add more game ending conditions here as needed
	// For example:
	// - Tournament time limit reached
	// - Maximum hands played
	// - All players but one eliminated
	// - etc.

	return false
}

// endGame ends the current game and transitions to WAITING_FOR_PLAYERS state
func (t *Table) endGame() {
	t.log.Infof("Ending game - not enough players remaining")

	// Clear the game
	t.game = nil

	// Reset all players to not ready
	for _, u := range t.users {
		u.IsReady = false
	}

	// Transition back to WAITING_FOR_PLAYERS state
	t.stateMachine.Dispatch(tableStateWaitingForPlayers)

	// Publish game ended event
	t.PublishEvent(pokerrpc.NotificationType_GAME_ENDED, t.config.ID, map[string]interface{}{
		"reason": "Not enough players remaining",
	})

	t.log.Infof("Game ended, table back to WAITING_FOR_PLAYERS state")
}

// startNewHand starts a fresh hand atomically (acquires the table lock internally)
func (t *Table) startNewHand() error {
	t.log.Debugf("startNewHand: Starting new hand")
	// Ensure hand setup is atomic for readers of table/game state
	// This prevents clients from observing partially-initialized new-hand state.
	t.mu.Lock()
	defer t.mu.Unlock()
	// Ensure game exists - if not, this is a bug
	if t.game == nil {
		return fmt.Errorf("startNewHand called but game is nil - this should not happen")
	}

	// Check if enough players still at table
	playersAtTable := len(t.users)

	// Allow heads-up play (2 players) in tournament mode even if original MinPlayers was higher
	minRequired := t.config.MinPlayers
	if playersAtTable >= 2 && playersAtTable < t.config.MinPlayers {
		minRequired = 2 // Allow heads-up play
	}

	if playersAtTable < minRequired {
		return fmt.Errorf("not enough players to start new hand: %d < %d", playersAtTable, minRequired)
	}

	// Get active users for the new hand - include all users (they will play all-in if needed)
	// (folded players will be reset for the new hand)
	activeUsers := make([]*User, 0, len(t.users))
	for _, u := range t.users {
		// Include all players - they will play all-in with their available chips if needed
		activeUsers = append(activeUsers, u)

		// Find the corresponding player to get their current poker chip balance for logging
		var playerBalance int64 = 0
		for _, player := range t.game.players {
			if player.id == u.ID {
				playerBalance = player.balance
				break
			}
		}

		if playerBalance >= t.config.BigBlind {
			t.log.Debugf("User %s eligible for new hand: pokerBalance=%d >= bigBlind=%d", u.ID, playerBalance, t.config.BigBlind)
		} else {
			t.log.Debugf("User %s will play all-in: pokerBalance=%d < bigBlind=%d", u.ID, playerBalance, t.config.BigBlind)
		}
	}

	// Sort users by TableSeat for consistent ordering
	sort.Slice(activeUsers, func(i, j int) bool {
		return activeUsers[i].TableSeat < activeUsers[j].TableSeat
	})

	// Reuse existing players but reset them for the new hand
	// First, reset existing players that are still active
	activePlayers := make([]*Player, 0, len(activeUsers))
	for _, user := range activeUsers {
		// Find the existing player object for this user
		var existingPlayer *Player
		for _, player := range t.game.players {
			if player.id == user.ID {
				existingPlayer = player
				break
			}
		}

		if existingPlayer != nil {
			// Reset the existing player for the new hand, preserving their current balance
			existingPlayer.ResetForNewHand(existingPlayer.balance)
			activePlayers = append(activePlayers, existingPlayer)
		} else {
			// This is a new player that joined between hands - create a new Player object
			newPlayer := NewPlayer(user.ID, user.Name, t.config.StartingChips)
			newPlayer.tableSeat = user.TableSeat
			newPlayer.isReady = user.IsReady
			activePlayers = append(activePlayers, newPlayer)
		}
	}

	// Update the game with the reused/reset players
	t.game.ResetForNewHand(activePlayers)

	// Use centralized hand setup logic (this assumes lock is held)
	err := t.setupNewHand(activeUsers)
	if err != nil {
		return fmt.Errorf("failed to setup new hand: %w", err)
	}

	// Reset showdown state for the new hand
	t.lastShowdown = nil
	t.resolvedRound = -1

	// Transition to game active state
	t.stateMachine.Dispatch(tableStateGameActive)

	t.lastAction = time.Now()
	return nil
}

// setupNewHand handles the complete setup process for a new hand (assumes lock is held)
func (t *Table) setupNewHand(activePlayers []*User) error {
	if t.game == nil {
		return fmt.Errorf("game not initialized")
	}
	if t.log == nil {
		t.log = slog.NewBackend(nil).Logger("TESTING")
		// return nil, fmt.Errorf("poker: log is required")
	}

	t.log.Debugf("setupNewHand: Starting hand setup for %d players", len(activePlayers))

	// Phase 1: Set to dealing phase (no broadcast yet - wait until setup is complete)
	t.game.phase = pokerrpc.GamePhase_NEW_HAND_DEALING
	t.log.Debugf("setupNewHand: Phase 1 - Set to NEW_HAND_DEALING, setup in progress")

	// Phase 2: Deal cards and post blinds (the actual setup work)
	t.log.Debugf("setupNewHand: Phase 2 - Dealing cards to %d players", len(activePlayers))
	err := t.dealCardsToPlayers(activePlayers)
	if err != nil {
		return fmt.Errorf("failed to deal cards: %v", err)
	}

	// Phase 2 continued: Post blinds
	t.log.Debugf("setupNewHand: Phase 2 - Posting blinds")
	err = t.postBlindsFromGame()
	if err != nil {
		return fmt.Errorf("failed to post blinds: %v", err)
	}

	// Phase 3: Transition to PRE_FLOP and set current player
	t.log.Debugf("setupNewHand: Phase 3 - Transitioning to PRE_FLOP and setting current player")
	t.game.phase = pokerrpc.GamePhase_PRE_FLOP
	t.initializeCurrentPlayer()
	t.log.Debugf("setupNewHand: Current player set to %s (index %d)", t.currentPlayerID(), t.game.GetCurrentPlayer())

	// Start the timeout clock for the first current player
	if t.game.currentPlayer >= 0 && t.game.currentPlayer < len(t.game.players) {
		if t.game.players[t.game.currentPlayer].GetCurrentStateString() != "FOLDED" {
			t.game.players[t.game.currentPlayer].lastAction = time.Now()
		}
	}

	// NO BROADCAST HERE - will be done by caller after state transition
	return nil
}

// GetStatus returns the current status of the table
func (t *Table) GetStatus() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	status := fmt.Sprintf("Table %s:\n", t.config.ID)
	status += fmt.Sprintf("Players: %d/%d\n", len(t.users), t.config.MaxPlayers)
	status += fmt.Sprintf("Buy-in: %.8f DCR\n", float64(t.config.BuyIn)/1e8)
	status += fmt.Sprintf("Starting Chips: %d chips\n", t.config.StartingChips)
	status += fmt.Sprintf("Blinds: %d/%d chips\n", t.config.SmallBlind, t.config.BigBlind)

	if t.game != nil {
		status += "Game in progress\n"
	} else {
		status += "Waiting for players\n"
	}

	return status
}

// GetUsers returns all users at the table
func (t *Table) GetUsers() []*User {
	t.mu.RLock()
	defer t.mu.RUnlock()

	users := make([]*User, 0, len(t.users))
	for _, u := range t.users {
		users = append(users, u)
	}

	// Sort by TableSeat to ensure consistent ordering
	sort.Slice(users, func(i, j int) bool {
		return users[i].TableSeat < users[j].TableSeat
	})

	return users
}

// GetBigBlind returns the big blind value for the table
func (t *Table) GetBigBlind() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.config.BigBlind
}

// MakeBet handles betting by delegating to the Game layer
func (t *Table) MakeBet(userID string, amount int64) error {
	if amount < 0 {
		return fmt.Errorf("amount cannot be negative")
	}

	t.mu.Lock()

	user := t.users[userID]
	if user == nil {
		t.mu.Unlock()
		return fmt.Errorf("user not found")
	}

	// Validate that it's this player's turn to act
	if t.isGameActive() && t.game != nil {
		currentPlayerID := t.currentPlayerID()
		t.log.Debugf("MakeBet: userID=%s, currentPlayerID=%s, currentPlayer=%d, gamePhase=%v, amount=%d",
			userID, currentPlayerID, t.game.GetCurrentPlayer(), t.game.GetPhase(), amount)
		if currentPlayerID != userID {
			t.mu.Unlock()
			return fmt.Errorf("not your turn to act")
		}

		// Delegate to Game layer - this handles all the betting logic (locks internally)
		if err := t.game.HandlePlayerBet(userID, amount); err != nil {
			t.mu.Unlock()
			return err
		}
	}

	t.lastAction = time.Now()
	t.mu.Unlock()

	// Check if this action completes the betting round (outside table lock)
	t.MaybeCompleteBettingRound()
	return nil
}

// GetMinPlayers returns the minimum number of players required
func (t *Table) GetMinPlayers() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.config.MinPlayers
}

// GetMaxPlayers returns the maximum number of players allowed
func (t *Table) GetMaxPlayers() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.config.MaxPlayers
}

// GetConfig returns the table configuration
func (t *Table) GetConfig() TableConfig {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.config
}

// GetGamePhase returns the current phase of the active game, or WAITING.
func (t *Table) GetGamePhase() pokerrpc.GamePhase {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.game == nil {
		return pokerrpc.GamePhase_WAITING
	}
	return t.game.GetPhase()
}

// HandleTimeouts iterates over players and auto-checks-or-folds those whose timebank expired.
func (t *Table) HandleTimeouts() {
	// Only run when game is active and TimeBank is positive
	if !t.isGameActive() || t.config.TimeBank == 0 || t.game == nil {
		return
	}

	now := time.Now()

	t.mu.Lock()

	// Only timeout the current player
	currentPlayerID := ""
	if t.game.currentPlayer >= 0 && t.game.currentPlayer < len(t.game.players) {
		// Prefer snapshot to avoid racy access to game internals
		snap := t.game.GetStateSnapshot()
		if snap.CurrentPlayer >= 0 && snap.CurrentPlayer < len(snap.Players) && snap.Players[snap.CurrentPlayer] != nil {
			currentPlayerID = snap.Players[snap.CurrentPlayer].id
		}
	}

	if currentPlayerID == "" {
		t.mu.Unlock()
		return // No current player to timeout
	}

	// The current player is in the unified player state (use actual pointer via index)
	snap := t.game.GetStateSnapshot()
	if snap.CurrentPlayer < 0 || snap.CurrentPlayer >= len(snap.Players) || snap.Players[snap.CurrentPlayer] == nil {
		t.mu.Unlock()
		return
	}
	// Access the actual player pointer for mutation
	currentIdx := snap.CurrentPlayer
	currentPlayer := t.game.players[currentIdx]
	if currentPlayer.GetCurrentStateString() == "FOLDED" || currentPlayer.GetCurrentStateString() == "ALL_IN" {
		t.mu.Unlock()
		return
	}

	// Whether we should check completion after releasing the table lock
	shouldCheckCompletion := false

	// Check if current player has timed out
	if now.Sub(currentPlayer.lastAction) > t.config.TimeBank {
		// Try to auto-check first, if not possible then auto-fold
		currentBet := snap.CurrentBet

		// A check is valid if the player's current bet equals the current bet
		// (meaning they don't need to put any additional money in)
		if currentPlayer.currentBet == currentBet {
			// Auto-check: essentially a bet of the current amount
			// This doesn't change the bet amounts but advances the action
			currentPlayer.lastAction = now

			// Increment actions counter for this betting round
			t.game.IncrementActionsInRound()

			// Advance to next player after check action
			t.advanceToNextPlayer()
		} else {
			// Auto-fold the current player - they cannot check because they need to call/raise
			// This covers the case where currentPlayer.currentBet < currentBet (player needs to call)
			currentPlayer.stateMachine.Dispatch(playerStateFolded)
			currentPlayer.lastAction = now

			// Advance to next player
			t.advanceToNextPlayer()
		}

		// Defer the completion check to run outside the table lock
		shouldCheckCompletion = true
	}

	t.mu.Unlock()

	if shouldCheckCompletion {
		t.MaybeCompleteBettingRound()
	}
}

// MaybeCompleteBettingRound delegates to Game layer for phase advancement logic
func (t *Table) MaybeCompleteBettingRound() error {
	if !t.isGameActive() || t.game == nil {
		return nil
	}

	// Compute actionable counts from a safe snapshot to avoid data races.
	snapshot := t.game.GetStateSnapshot()
	alivePlayers := 0
	activePlayers := 0
	for _, p := range snapshot.Players {
		if p == nil {
			continue
		}
		if p.GetCurrentStateString() != "FOLDED" {
			alivePlayers++
			if p.GetCurrentStateString() != "ALL_IN" {
				activePlayers++
			}
		}
	}

	var err error

	if alivePlayers > 1 && (activePlayers == 0 || activePlayers == 1) {
		// Step through missing streets asynchronously with small gaps so
		// snapshots captured by the server reflect each intermediate phase.
		startPhase := t.game.GetPhase()
		tableID := t.config.ID
		ap := activePlayers
		al := alivePlayers
		go func() {
			// Refund any uncalled portion before we reset current bets by dealing
			// additional streets. This avoids creating invalid side pots that only
			// the all-in player is eligible to win.
			if localErr := t.game.RefundUncalledBets(); localErr != nil {
				t.log.Errorf("table.maybeAdvancePhase: failed to refund uncalled bets: %v", localErr)
			}
			step := func(do func(), note string) {
				do()
				t.log.Debugf("table.maybeAdvancePhase: broadcast %s", note)
				t.PublishEvent(pokerrpc.NotificationType_NEW_ROUND, tableID, nil)
				time.Sleep(1 * time.Second)
			}
			switch startPhase {
			case pokerrpc.GamePhase_PRE_FLOP:
				step(t.game.StateFlop, "FLOP")
				step(t.game.StateTurn, "TURN")
				step(t.game.StateRiver, "RIVER")
			case pokerrpc.GamePhase_FLOP:
				step(t.game.StateTurn, "TURN")
				step(t.game.StateRiver, "RIVER")
			case pokerrpc.GamePhase_TURN:
				step(t.game.StateRiver, "RIVER")
			}
			t.log.Debugf("table.maybeAdvancePhase: betting closed (alive=%d active=%d), proceeding to SHOWDOWN with broadcasts", al, ap)
			// Proceed to showdown (phase will be set by game logic inside handleShowdown).
			_ = t.handleShowdown()
		}()
		return nil
	}

	// Otherwise, delegate to Game layer for normal progression
	t.log.Debugf("table.maybeAdvancePhase: delegating (phase=%v actionsInRound=%d currentBet=%d)", t.game.GetPhase(), t.game.GetActionsInRound(), t.game.GetCurrentBet())
	t.game.maybeCompleteBettingRound()

	// Handle showdown if we reached that phase
	if t.game.GetPhase() == pokerrpc.GamePhase_SHOWDOWN {
		t.log.Debugf("table.maybeAdvancePhase: entering SHOWDOWN, handling showdown")
		err = t.handleShowdown()
		if err != nil {
			t.log.Errorf("table.maybeAdvancePhase: failed to handle showdown: %v", err)
			return err
		}
	}

	return err
}

// GetGame returns the current game (can be nil)
func (t *Table) GetGame() *Game {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.game
}

// GetLastShowdown returns the last recorded showdown result (if any).
func (t *Table) GetLastShowdown() *ShowdownResult {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastShowdown
}

// GetCurrentBet returns the current highest bet for the ongoing betting round.
// If no game is active it returns zero.
func (t *Table) GetCurrentBet() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.game == nil {
		return 0
	}
	return t.game.GetCurrentBet()
}

// GetCurrentPlayerID returns the ID of the player whose turn it is
func (t *Table) GetCurrentPlayerID() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	// Safely derive from a snapshot
	if t.game == nil {
		return ""
	}
	snap := t.game.GetStateSnapshot()
	if snap.CurrentPlayer < 0 || snap.CurrentPlayer >= len(snap.Players) {
		return ""
	}
	p := snap.Players[snap.CurrentPlayer]
	if p == nil {
		return ""
	}
	return p.id
}

// currentPlayerID returns the current player ID without acquiring locks (private helper)
func (t *Table) currentPlayerID() string {
	if t.game == nil {
		return ""
	}
	snap := t.game.GetStateSnapshot()
	if snap.CurrentPlayer < 0 || snap.CurrentPlayer >= len(snap.Players) {
		return ""
	}
	p := snap.Players[snap.CurrentPlayer]
	if p == nil {
		return ""
	}
	return p.id
}

// advanceToNextPlayer delegates to Game layer
func (t *Table) advanceToNextPlayer() {
	if t.game == nil {
		return
	}
	t.game.AdvanceToNextPlayer()
}

// initializeCurrentPlayer delegates to Game layer
func (t *Table) initializeCurrentPlayer() {
	if t.game == nil {
		return
	}
	t.game.InitializeCurrentPlayer()
}

// HandleFold handles folding by delegating to the Game layer
func (t *Table) HandleFold(userID string) error {
	t.mu.Lock()

	user := t.users[userID]
	if user == nil {
		t.mu.Unlock()
		return fmt.Errorf("user not found")
	}

	// Validate that it's this player's turn to act
	if t.isGameActive() && t.game != nil {
		currentPlayerID := t.currentPlayerID()
		t.log.Debugf("HandleFold: userID=%s, currentPlayerID=%s, currentPlayer=%d, gamePhase=%v",
			userID, currentPlayerID, t.game.GetCurrentPlayer(), t.game.GetPhase())
		if currentPlayerID != userID {
			t.mu.Unlock()
			return fmt.Errorf("not your turn to act")
		}

		// Delegate to Game layer - this handles all the folding logic (locks internally)
		if err := t.game.HandlePlayerFold(userID); err != nil {
			t.mu.Unlock()
			return err
		}
	}

	t.lastAction = time.Now()
	t.mu.Unlock()

	// Check if this action completes the betting round (outside table lock)
	t.MaybeCompleteBettingRound()
	return nil
}

// HandleCall handles call actions by delegating to the Game layer
func (t *Table) HandleCall(userID string) error {
	t.mu.Lock()

	user := t.users[userID]
	if user == nil {
		t.mu.Unlock()
		return fmt.Errorf("user not found")
	}

	// Validate that it's this player's turn to act
	if t.isGameActive() && t.game != nil {
		currentPlayerID := t.currentPlayerID()
		t.log.Debugf("HandleCall: userID=%s, currentPlayerID=%s, currentPlayer=%d, gamePhase=%v",
			userID, currentPlayerID, t.game.GetCurrentPlayer(), t.game.GetPhase())
		if currentPlayerID != userID {
			t.mu.Unlock()
			return fmt.Errorf("not your turn to act")
		}

		// Delegate to Game layer - this handles all the calling logic (locks internally)
		if err := t.game.HandlePlayerCall(userID); err != nil {
			t.mu.Unlock()
			return err
		}

		t.log.Debugf("HandleCall: user %s called; actionsInRound=%d currentBet=%d", userID, t.game.GetActionsInRound(), t.game.GetCurrentBet())
	}

	t.lastAction = time.Now()
	t.mu.Unlock()

	// Check if this action completes the betting round (outside table lock)
	t.MaybeCompleteBettingRound()
	return nil
}

// HandleCheck handles check actions by delegating to the Game layer
func (t *Table) HandleCheck(userID string) error {
	t.mu.Lock()

	user := t.users[userID]
	if user == nil {
		t.mu.Unlock()
		return fmt.Errorf("user not found")
	}

	// Validate that it's this player's turn to act
	if t.isGameActive() && t.game != nil {
		currentPlayerID := t.currentPlayerID()
		t.log.Debugf("HandleCheck: userID=%s, currentPlayerID=%s, currentPlayer=%d, gamePhase=%v",
			userID, currentPlayerID, t.game.GetCurrentPlayer(), t.game.GetPhase())
		if currentPlayerID != userID {
			t.mu.Unlock()
			return fmt.Errorf("not your turn to act")
		}

		// Delegate to Game layer - this handles all the checking logic (locks internally)
		if err := t.game.HandlePlayerCheck(userID); err != nil {
			t.mu.Unlock()
			return err
		}

		t.log.Debugf("HandleCheck: user %s checked; actionsInRound=%d currentBet=%d", userID, t.game.GetActionsInRound(), t.game.GetCurrentBet())
	}

	t.lastAction = time.Now()
	t.mu.Unlock()

	// Check if this action completes the betting round (outside table lock)
	err := t.MaybeCompleteBettingRound()
	if err != nil {
		t.log.Errorf("HandleCheck: failed to complete betting round: %v", err)
		return err
	}
	return nil
}

// postBlindsFromGame calls the game state machine logic to post blinds
func (t *Table) postBlindsFromGame() error {
	if t.game == nil {
		return fmt.Errorf("game not started")
	}

	numPlayers := len(t.game.players)
	if numPlayers < 2 {
		return fmt.Errorf("not enough players for blinds")
	}

	// Calculate blind positions
	smallBlindPos := (t.game.dealer + 1) % numPlayers
	bigBlindPos := (t.game.dealer + 2) % numPlayers

	// For heads-up (2 players), dealer posts small blind
	if numPlayers == 2 {
		smallBlindPos = t.game.dealer
		bigBlindPos = (t.game.dealer + 1) % numPlayers
	}

	t.log.Debugf("postBlindsFromGame: numPlayers=%d, dealer=%d, smallBlindPos=%d, bigBlindPos=%d",
		numPlayers, t.game.dealer, smallBlindPos, bigBlindPos)

	// Post small blind
	if t.game.players[smallBlindPos] != nil {
		smallBlindAmount := t.game.config.SmallBlind
		player := t.game.players[smallBlindPos]

		// Handle all-in logic for small blind
		if smallBlindAmount > player.balance {
			// Player cannot cover small blind - treat as all-in of remaining balance
			smallBlindAmount = player.balance
			player.stateMachine.Dispatch(playerStateAllIn)
			t.log.Debugf("Player %s all-in for small blind: posting %d (had %d)", player.id, smallBlindAmount, player.balance)
		}

		player.balance -= smallBlindAmount
		player.currentBet = smallBlindAmount
		t.game.potManager.addBet(smallBlindPos, smallBlindAmount, t.game.players)

		// Send small blind notification
		// DISABLED: Notification callbacks cause deadlocks - server handles notifications directly
		// go t.eventManager.NotifyBlindPosted(t.config.ID, t.game.players[smallBlindPos].ID, smallBlindAmount, true)
	}

	// Post big blind
	if t.game.players[bigBlindPos] != nil {
		bigBlindAmount := t.game.config.BigBlind
		player := t.game.players[bigBlindPos]

		// Handle all-in logic for big blind
		if bigBlindAmount > player.balance {
			// Player cannot cover big blind - treat as all-in of remaining balance
			bigBlindAmount = player.balance
			player.stateMachine.Dispatch(playerStateAllIn)
			t.log.Debugf("Player %s all-in for big blind: posting %d (had %d)", player.id, bigBlindAmount, player.balance)
		}

		player.balance -= bigBlindAmount
		player.currentBet = bigBlindAmount
		t.game.potManager.addBet(bigBlindPos, bigBlindAmount, t.game.players)
		t.game.currentBet = bigBlindAmount // Set current bet to big blind amount

		// Send big blind notification
	}

	return nil
}

// dealCardsToPlayers deals cards to active players using the unified player state
func (t *Table) dealCardsToPlayers(activePlayers []*User) error {
	if t.game == nil || t.game.deck == nil {
		return fmt.Errorf("game or deck not initialized")
	}

	// Deal 2 cards to each active player
	for i := 0; i < 2; i++ {
		for _, u := range activePlayers {
			card, ok := t.game.deck.Draw()
			if !ok {
				return fmt.Errorf("failed to deal card to user %s: deck is empty", u.ID)
			}

			// Also sync the card to the corresponding game player
			found := false
			for _, player := range t.game.players {
				if player.id == u.ID {
					player.hand = append(player.hand, card)
					found = true
					break
				}
			}

			if !found {
				t.log.Debugf("DEBUG: Could not find game player for user %s when dealing cards", u.ID)
			} else {

			}
		}
	}
	return nil
}

// AddUser adds a user to the table
func (t *Table) AddUser(user *User) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check if table is full
	if len(t.users) >= t.config.MaxPlayers {
		return fmt.Errorf("table is full")
	}

	// Check if user already at table
	if _, exists := t.users[user.ID]; exists {
		return fmt.Errorf("user already at table")
	}

	t.users[user.ID] = user
	t.lastAction = time.Now()
	return nil
}

// AddNewUser creates and adds a new user to the table in one operation
func (t *Table) AddNewUser(id, name string, dcrAccountBalance int64, seat int) (*User, error) {
	user := NewUser(id, name, dcrAccountBalance, seat)
	err := t.AddUser(user)
	if err != nil {
		return nil, err
	}
	return user, nil
}

// RemoveUser removes a user from the table
func (t *Table) RemoveUser(userID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.users[userID]; !exists {
		return fmt.Errorf("user not at table")
	}

	delete(t.users, userID)
	t.lastAction = time.Now()
	return nil
}

// removeUserWithoutLock removes a user from the table without acquiring the lock
// This is used internally when the caller already holds the table lock
func (t *Table) removeUserWithoutLock(userID string) error {
	if _, exists := t.users[userID]; !exists {
		return fmt.Errorf("user not at table")
	}

	delete(t.users, userID)
	t.lastAction = time.Now()
	return nil
}

// GetUser returns a user by ID
func (t *Table) GetUser(userID string) *User {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.users[userID]
}

// SetHost transfers host ownership to a new user
func (t *Table) SetHost(newHostID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Verify the new host is actually at the table
	if _, exists := t.users[newHostID]; !exists {
		return fmt.Errorf("new host %s is not at the table", newHostID)
	}

	// Update the host ID in the config
	t.config.HostID = newHostID
	t.lastAction = time.Now()

	return nil
}

// SetPlayerReady sets the ready status for a player
func (t *Table) SetPlayerReady(userID string, ready bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	user := t.users[userID]
	if user == nil {
		return fmt.Errorf("user not found at table")
	}

	user.IsReady = ready
	return nil
}

// TableStateSnapshot represents a point-in-time snapshot of table state for safe concurrent access
type TableStateSnapshot struct {
	Config      TableConfig
	Users       []*User
	GameStarted bool
	GamePhase   pokerrpc.GamePhase
	Game        *GameStateSnapshot // Nested game state snapshot if game is active
}

// GetStateSnapshot returns an atomic snapshot of the table state for safe concurrent access
func (t *Table) GetStateSnapshot() TableStateSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Create a deep copy of users to avoid race conditions
	usersCopy := make([]*User, 0, len(t.users))
	for _, user := range t.users {
		userCopy := &User{
			ID:                user.ID,
			Name:              user.Name,
			DCRAccountBalance: user.DCRAccountBalance,
			TableSeat:         user.TableSeat,
			IsReady:           user.IsReady,
			JoinedAt:          user.JoinedAt,
		}
		usersCopy = append(usersCopy, userCopy)
	}

	// Sort by TableSeat to ensure consistent ordering
	sort.Slice(usersCopy, func(i, j int) bool {
		return usersCopy[i].TableSeat < usersCopy[j].TableSeat
	})

	// Get game state snapshot if game is active
	var gameSnapshot *GameStateSnapshot
	if t.game != nil {
		snapshot := t.game.GetStateSnapshot()
		gameSnapshot = &snapshot
	}

	return TableStateSnapshot{
		Config:      t.config,
		Users:       usersCopy,
		GameStarted: t.game != nil,
		GamePhase:   t.getGamePhase(),
		Game:        gameSnapshot,
	}
}

// getGamePhase returns the current phase without acquiring locks (private helper)
func (t *Table) getGamePhase() pokerrpc.GamePhase {
	if t.game == nil {
		return pokerrpc.GamePhase_WAITING
	}
	return t.game.GetPhase()
}

// SetUserDCRAccountBalance safely updates the DCRAccountBalance of a user seated at the table.
// It acquires the table lock to synchronize concurrent access so that readers (e.g. state snapshots)
// don't race with writers like JoinTable.
func (t *Table) SetUserDCRAccountBalance(userID string, newBalance int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	u, ok := t.users[userID]
	if !ok {
		return fmt.Errorf("user not found at table")
	}

	u.DCRAccountBalance = newBalance
	return nil
}

// RestoreGame replaces the current game pointer with a previously reconstructed
// *Game instance during table restoration. It sets the table state to
// GAME_ACTIVE without running any of the normal new-hand setup logic. This is
// intended to be used exclusively by the server layer when rebuilding tables
// from persisted snapshots.
func (t *Table) RestoreGame(g *Game) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Directly set the game instance.
	t.game = g

	// Ensure the table state reflects that an active game is in progress so
	// that other table methods (IsGameStarted, etc.) behave correctly.
	t.stateMachine.Dispatch(tableStateGameActive)
}

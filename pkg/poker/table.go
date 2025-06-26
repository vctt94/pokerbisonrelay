package poker

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/decred/slog"

	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/poker-bisonrelay/pkg/statemachine"
)

// TableStateFn represents a table state function following Rob Pike's pattern
type TableStateFn = statemachine.StateFn[Table]

// User represents someone seated at the table (not necessarily playing)
type User struct {
	ID             string
	Name           string
	AccountBalance int64 // DCR account balance (in atoms)
	TableSeat      int   // Seat position at the table
	IsReady        bool  // Ready to start/continue games
	JoinedAt       time.Time
	LastAction     time.Time

	// Game-related temporary fields (used during active games)
	HasBet    int64  // Current bet amount in this betting round
	HasFolded bool   // Folded in current hand
	Hand      []Card // Cards in hand during game
}

// NewUser creates a new user
func NewUser(id, name string, accountBalance int64, seat int) *User {
	return &User{
		ID:             id,
		Name:           name,
		AccountBalance: accountBalance,
		TableSeat:      seat,
		IsReady:        false,
		JoinedAt:       time.Now(),
		LastAction:     time.Now(),
		HasBet:         0,
		HasFolded:      false,
		Hand:           make([]Card, 0, 2),
	}
}

// IsActiveInGame returns true if the user is actively in the current game
func (u *User) IsActiveInGame() bool {
	return !u.HasFolded && u.IsReady
}

// ResetForNewHand resets the user's game state for a new hand
func (u *User) ResetForNewHand(startingChips int64) {
	u.Hand = make([]Card, 0, 2)
	u.HasBet = 0
	u.HasFolded = false
	u.LastAction = time.Now()
}

// SetGameState updates the user's game state (simplified for users)
func (u *User) SetGameState(stateName string) {
	switch stateName {
	case "FOLDED":
		u.HasFolded = true
	case "ALL_IN":
		// Handle all-in state
	}
}

// TableConfig holds configuration for a new poker table
type TableConfig struct {
	ID             string
	Log            slog.Logger
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

// NotificationSender is an interface for sending notifications
type NotificationSender interface {
	SendAllPlayersReady(tableID string)
	SendGameStarted(tableID string)
	SendNewHandStarted(tableID string)
	SendPlayerReady(tableID, playerID string, ready bool)
	SendBlindPosted(tableID, playerID string, amount int64, isSmallBlind bool)
	BroadcastGameStateUpdate(tableID string)
	SendShowdownResult(tableID string, winners []*pokerrpc.Winner, pot int64)
}

// TableEventManager handles notifications and state updates for table events
type TableEventManager struct {
	notificationSender NotificationSender
}

// NewTableEventManager creates a new event manager
func NewTableEventManager(notificationSender NotificationSender) *TableEventManager {
	return &TableEventManager{
		notificationSender: notificationSender,
	}
}

// NotifyPlayerReady sends player ready notification
func (tem *TableEventManager) NotifyPlayerReady(tableID, playerID string, ready bool) {
	if tem.notificationSender != nil {
		tem.notificationSender.SendPlayerReady(tableID, playerID, ready)
		tem.notificationSender.BroadcastGameStateUpdate(tableID)
	}
}

// NotifyAllPlayersReady sends all players ready notification
func (tem *TableEventManager) NotifyAllPlayersReady(tableID string) {
	if tem.notificationSender != nil {
		tem.notificationSender.SendAllPlayersReady(tableID)
		tem.notificationSender.BroadcastGameStateUpdate(tableID)
	}
}

// NotifyGameStarted sends game started notification
func (tem *TableEventManager) NotifyGameStarted(tableID string) {
	if tem.notificationSender != nil {
		tem.notificationSender.SendGameStarted(tableID)
	}
}

// NotifyNewHandStarted sends new hand started notification
func (tem *TableEventManager) NotifyNewHandStarted(tableID string) {
	if tem.notificationSender != nil {
		tem.notificationSender.SendNewHandStarted(tableID)
	}
}

// NotifyBlindPosted sends blind posted notification
func (tem *TableEventManager) NotifyBlindPosted(tableID, playerID string, amount int64, isSmallBlind bool) {
	if tem.notificationSender != nil {
		tem.notificationSender.SendBlindPosted(tableID, playerID, amount, isSmallBlind)
	}
}

// Table represents a poker table that manages users and delegates game logic to Game
type Table struct {
	log        slog.Logger
	config     TableConfig
	users      map[string]*User // Users seated at the table
	game       *Game            // Game logic that handles all player management
	mu         sync.RWMutex
	createdAt  time.Time
	lastAction time.Time
	// Event manager for notifications
	eventManager *TableEventManager
	// Auto-start management
	autoStartTimer    *time.Timer
	autoStartCanceled bool

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
func tableStateWaitingForPlayers(entity *Table, callback func(stateName string, event statemachine.StateEvent)) TableStateFn {
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
			if callback != nil {
				callback("PLAYERS_READY", statemachine.StateEntered)
			}
			return tableStatePlayersReady
		}
	}

	if callback != nil {
		callback("WAITING_FOR_PLAYERS", statemachine.StateEntered)
	}
	return tableStateWaitingForPlayers // Stay in this state
}

// tableStatePlayersReady handles the PLAYERS_READY state logic
func tableStatePlayersReady(entity *Table, callback func(stateName string, event statemachine.StateEvent)) TableStateFn {
	// Send notification that all players are ready
	if entity.eventManager.notificationSender != nil {
		go entity.eventManager.NotifyAllPlayersReady(entity.config.ID)
	}

	if callback != nil {
		callback("PLAYERS_READY", statemachine.StateEntered)
	}
	// This state waits for external trigger (StartGame)
	return tableStatePlayersReady
}

// tableStateGameActive handles the GAME_ACTIVE state logic
func tableStateGameActive(entity *Table, callback func(stateName string, event statemachine.StateEvent)) TableStateFn {
	// Check if game should move to showdown
	if entity.game != nil && entity.game.GetPhase() == pokerrpc.GamePhase_SHOWDOWN {
		if callback != nil {
			callback("SHOWDOWN", statemachine.StateEntered)
		}
		return tableStateShowdown
	}

	if callback != nil {
		callback("GAME_ACTIVE", statemachine.StateEntered)
	}
	return tableStateGameActive // Stay in this state during normal gameplay
}

// tableStateShowdown handles the SHOWDOWN state logic
func tableStateShowdown(entity *Table, callback func(stateName string, event statemachine.StateEvent)) TableStateFn {
	// The actual showdown logic is handled by handleShowdown()
	// This state manages the transition to cleanup
	if callback != nil {
		callback("SHOWDOWN", statemachine.StateEntered)
	}
	return tableStateCleanup
}

// tableStateCleanup handles the CLEANUP state logic
func tableStateCleanup(entity *Table, callback func(stateName string, event statemachine.StateEvent)) TableStateFn {
	// Check if we can start a new hand
	playersReadyForNextHand := 0
	for _, u := range entity.users {
		if u.AccountBalance >= entity.config.BigBlind {
			playersReadyForNextHand++
		}
	}

	if playersReadyForNextHand >= entity.config.MinPlayers {
		// Can start new hand - transition back to game active
		if callback != nil {
			callback("GAME_ACTIVE", statemachine.StateEntered)
		}
		return tableStateGameActive
	} else {
		// Not enough players - go back to waiting
		if callback != nil {
			callback("WAITING_FOR_PLAYERS", statemachine.StateEntered)
		}
		return tableStateWaitingForPlayers
	}
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
	case fmt.Sprintf("%p", tableStateShowdown):
		return "SHOWDOWN"
	case fmt.Sprintf("%p", tableStateCleanup):
		return "CLEANUP"
	default:
		return "UNKNOWN"
	}
}

// CheckAllPlayersReady simplified - just triggers state machine update
func (t *Table) CheckAllPlayersReady() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Let the state machine handle the logic with broadcast callback
	t.stateMachine.Dispatch(func(stateName string, event statemachine.StateEvent) {
		if event == statemachine.StateEntered {
			t.broadcastGameStateUpdate()
		}
	})

	// Check the resulting state
	state := t.GetTableStateString()
	return state == "PLAYERS_READY" || state == "GAME_ACTIVE" || state == "SHOWDOWN"
}

// StartGame starts a new game at the table using the state machine
func (t *Table) StartGame() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check if we're in the right state
	if t.GetTableStateString() != "PLAYERS_READY" {
		return fmt.Errorf("cannot start game: table not in PLAYERS_READY state")
	}

	// Cancel any pending auto-start since we're manually starting
	t.cancelAutoStart()

	// Check if we have enough players
	if len(t.users) < t.config.MinPlayers {
		return fmt.Errorf("not enough players to start game")
	}

	// Reset all players for the new hand
	activePlayers := make([]*User, 0, len(t.users))
	for _, u := range t.users {
		u.ResetForNewHand(t.config.StartingChips)
		activePlayers = append(activePlayers, u)
	}

	// Sort players by TableSeat for consistent ordering
	sort.Slice(activePlayers, func(i, j int) bool {
		return activePlayers[i].TableSeat < activePlayers[j].TableSeat
	})

	// Create a new game - players are managed by the table
	t.game = NewGame(GameConfig{
		NumPlayers:    len(activePlayers),
		StartingChips: t.config.StartingChips,
		SmallBlind:    t.config.SmallBlind,
		BigBlind:      t.config.BigBlind,
	})
	// Set the players in the game to reference the same objects from the table
	t.game.SetPlayers(activePlayers)

	// Use centralized hand setup logic (this assumes lock is held)
	err := t.setupNewHand(activePlayers)
	if err != nil {
		return err
	}

	// Transition to game active state with broadcast callback
	t.stateMachine.SetState(tableStateGameActive)

	// SINGLE broadcast at the end after all setup is complete
	t.broadcastGameStateUpdate()

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
func (t *Table) handleShowdown() {
	if t.game == nil {
		return
	}

	// Delegate all showdown logic to the game
	result := t.game.handleShowdown()

	// Sync player balances back to users
	t.game.syncBalancesToUsers(t.users)

	// Send showdown result notification to all players
	if t.eventManager.notificationSender != nil && len(result.WinnerInfo) > 0 {
		t.eventManager.notificationSender.SendShowdownResult(t.config.ID, result.WinnerInfo, result.TotalPot)
		// Note: BroadcastGameStateUpdate is handled by the calling action method to avoid duplicates
	}

	// Count players still at the table with sufficient balance for the next hand
	playersReadyForNextHand := 0
	for _, u := range t.users {
		if u.AccountBalance >= t.config.BigBlind {
			playersReadyForNextHand++
		}
	}

	// Reset user betting state (game handles its own player state)
	for _, u := range t.users {
		u.HasFolded = false
		u.HasBet = 0
		// Keep u.Hand for showdown viewing by users
	}

	t.game.ResetActionsInRound()
	t.lastAction = time.Now()

	// Transition to showdown state with broadcast callback
	t.stateMachine.SetState(tableStateShowdown)

	// Auto-start next hand after configured delay if enough players remain
	if playersReadyForNextHand >= t.config.MinPlayers && t.config.AutoStartDelay > 0 {
		t.scheduleAutoStart()
	}
}

// startNewHand is the internal implementation that assumes the lock is already held
func (t *Table) startNewHand() error {
	// Ensure game exists - if not, this is a bug
	if t.game == nil {
		return fmt.Errorf("startNewHand called but game is nil - this should not happen")
	}

	// Check if enough players still at table
	playersAtTable := len(t.users)

	if playersAtTable < t.config.MinPlayers {
		return fmt.Errorf("not enough players to start new hand")
	}

	// Get active users for the new hand
	activeUsers := make([]*User, 0, len(t.users))
	for _, u := range t.users {
		if u.AccountBalance >= t.config.BigBlind {
			activeUsers = append(activeUsers, u)
		}
	}

	// Sort users by TableSeat for consistent ordering
	sort.Slice(activeUsers, func(i, j int) bool {
		return activeUsers[i].TableSeat < activeUsers[j].TableSeat
	})

	// Reset all users' hand-specific state from previous hand
	for _, u := range t.users {
		oldHandSize := len(u.Hand)
		u.ResetForNewHand(u.AccountBalance) // Use consistent reset method
		// Note: ResetForNewHand already handles IsAllIn, IsTurn, IsDealer
		t.log.Debugf("startNewHand: reset user %s - old hand size: %d, new hand size: %d", u.ID, oldHandSize, len(u.Hand))
	}

	// Reuse existing players but reset them for the new hand
	// First, reset existing players that are still active
	activePlayers := make([]*Player, 0, len(activeUsers))
	for _, user := range activeUsers {
		// Find the existing player object for this user
		var existingPlayer *Player
		for _, player := range t.game.players {
			if player.ID == user.ID {
				existingPlayer = player
				break
			}
		}

		if existingPlayer != nil {
			// Reset the existing player for the new hand
			existingPlayer.ResetForNewHand(t.config.StartingChips)
			activePlayers = append(activePlayers, existingPlayer)
		} else {
			// This is a new player that joined between hands - create a new Player object
			newPlayer := NewPlayer(user.ID, user.Name, t.config.StartingChips)
			newPlayer.AccountBalance = user.AccountBalance
			newPlayer.TableSeat = user.TableSeat
			newPlayer.IsReady = user.IsReady
			newPlayer.LastAction = user.LastAction
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

	// Transition to game active state
	t.stateMachine.SetState(tableStateGameActive)

	// SINGLE consolidated broadcast FIRST to ensure players get the new game state
	// This ensures players can see their cards and current player correctly
	t.broadcastGameStateUpdate()

	// Send new hand notification AFTER the game state update
	// This prevents the UI from clearing state before receiving the new state
	go t.eventManager.NotifyNewHandStarted(t.config.ID)

	t.lastAction = time.Now()
	return nil
}

// scheduleAutoStart schedules automatic start of next hand after configured delay
func (t *Table) scheduleAutoStart() {
	// Cancel any existing auto-start timer
	t.cancelAutoStart()

	// Mark that auto-start is pending
	t.autoStartCanceled = false

	// Schedule the auto-start
	t.autoStartTimer = time.AfterFunc(t.config.AutoStartDelay, func() {
		t.mu.Lock()
		defer t.mu.Unlock()

		// Check if auto-start was canceled
		if t.autoStartCanceled {
			return
		}

		// Double-check we still have enough players
		playersReadyForNextHand := 0
		for _, u := range t.users {
			if u.AccountBalance >= t.config.BigBlind {
				playersReadyForNextHand++
			}
		}

		if playersReadyForNextHand >= t.config.MinPlayers {
			t.log.Debugf("Auto-starting new hand with %d players after showdown", playersReadyForNextHand)
			err := t.startNewHand() // Use internal version since we already hold the lock
			if err != nil {
				t.log.Debugf("Auto-start new hand failed: %v", err)
			} else {
				t.log.Debugf("Auto-started new hand successfully with %d players", playersReadyForNextHand)
			}
		} else {
			t.log.Debugf("Not enough players for auto-start: %d < %d", playersReadyForNextHand, t.config.MinPlayers)
		}
	})
}

// cancelAutoStart cancels any pending auto-start timer
func (t *Table) cancelAutoStart() {
	if t.autoStartTimer != nil {
		t.autoStartTimer.Stop()
		t.autoStartTimer = nil
	}
	t.autoStartCanceled = true
}

// broadcastGameStateUpdate broadcasts game state update to all players
func (t *Table) broadcastGameStateUpdate() {
	if t.eventManager.notificationSender != nil {
		// Use asynchronous broadcast to avoid holding locks
		go t.eventManager.notificationSender.BroadcastGameStateUpdate(t.config.ID)
	}
}

// setupNewHand handles the complete setup process for a new hand (assumes lock is held)
func (t *Table) setupNewHand(activePlayers []*User) error {
	if t.game == nil {
		return fmt.Errorf("game not initialized")
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
	t.log.Debugf("setupNewHand: Current player set to %s (index %d)", t.currentPlayerID(), t.game.currentPlayer)

	// Start the timeout clock for the first current player
	if t.game.currentPlayer >= 0 && t.game.currentPlayer < len(t.game.players) {
		if !t.game.players[t.game.currentPlayer].HasFolded {
			t.game.players[t.game.currentPlayer].LastAction = time.Now()
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
	t.mu.Lock()
	defer t.mu.Unlock()

	user := t.users[userID]
	if user == nil {
		return fmt.Errorf("user not found")
	}

	// Validate that it's this player's turn to act
	if t.isGameActive() && t.game != nil {
		currentPlayerID := t.currentPlayerID()
		t.log.Debugf("MakeBet: userID=%s, currentPlayerID=%s, currentPlayer=%d, gamePhase=%v, amount=%d",
			userID, currentPlayerID, t.game.currentPlayer, t.game.phase, amount)
		if currentPlayerID != userID {
			return fmt.Errorf("not your turn to act")
		}

		// Delegate to Game layer - this handles all the betting logic
		err := t.game.handlePlayerBet(userID, amount)
		if err != nil {
			return err
		}

		// Sync the updated player state back to user using state machine dispatch
		if err := t.syncPlayerState(userID); err != nil {
			t.log.Errorf("Failed to sync player state after bet: %v", err)
		}

		// Check if this action completes the betting round
		t.maybeAdvancePhase()
	}

	// SINGLE broadcast at the end of all processing
	t.broadcastGameStateUpdate()

	t.lastAction = time.Now()
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
	defer t.mu.Unlock()

	// Only timeout the current player
	currentPlayerID := ""
	if t.game.currentPlayer >= 0 && t.game.currentPlayer < len(t.game.players) {
		currentPlayerID = t.game.players[t.game.currentPlayer].ID
	}

	if currentPlayerID == "" {
		return // No current player to timeout
	}

	// The current player is already in our unified player state
	currentPlayer := t.game.players[t.game.currentPlayer]
	if currentPlayer.HasFolded {
		return
	}

	// Check if current player has timed out
	if now.Sub(currentPlayer.LastAction) > t.config.TimeBank {
		// Try to auto-check first, if not possible then auto-fold
		currentBet := t.game.currentBet

		// A check is valid if the player's current bet equals the current bet
		// (meaning they don't need to put any additional money in)
		if currentPlayer.HasBet == currentBet {
			// Auto-check: essentially a bet of the current amount
			// This doesn't change the bet amounts but advances the action
			currentPlayer.LastAction = now

			// Increment actions counter for this betting round
			t.game.IncrementActionsInRound()

			// Advance to next player after check action
			t.advanceToNextPlayer()
		} else {
			// Auto-fold the current player - they cannot check because they need to call/raise
			// This covers the case where currentPlayer.HasBet < currentBet (player needs to call)
			currentPlayer.HasFolded = true
			currentPlayer.LastAction = now

			// Advance to next player
			t.advanceToNextPlayer()
		}

		// Check if this action completes the betting round
		t.maybeAdvancePhase()
	}
}

// maybeAdvancePhase delegates to Game layer for phase advancement logic
func (t *Table) maybeAdvancePhase() {
	if !t.isGameActive() || t.game == nil {
		return
	}

	// Delegate to Game layer - this handles all the phase advancement logic
	t.game.maybeAdvancePhase()

	// Handle showdown if we reached that phase
	if t.game.phase == pokerrpc.GamePhase_SHOWDOWN {
		t.handleShowdown()
	}

	// Note: BroadcastGameStateUpdate is handled by the calling action method
	// (HandleCheck, HandleCall, HandleFold, MakeBet) to avoid duplicate broadcasts
}

// GetGame returns the active game instance (if any).
func (t *Table) GetGame() *Game {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.game
}

// GetCurrentBet returns the current highest bet for the ongoing betting round.
// If no game is active it returns zero.
func (t *Table) GetCurrentBet() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.game == nil {
		return 0
	}
	return t.game.currentBet
}

// GetCurrentPlayerID returns the ID of the player whose turn it is
func (t *Table) GetCurrentPlayerID() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.currentPlayerID()
}

// currentPlayerID returns the current player ID without acquiring locks (private helper)
func (t *Table) currentPlayerID() string {
	if t.game == nil || len(t.game.players) == 0 {
		return ""
	}

	if t.game.currentPlayer < 0 || t.game.currentPlayer >= len(t.game.players) {
		return ""
	}

	return t.game.players[t.game.currentPlayer].ID
}

// advanceToNextPlayer delegates to Game layer
func (t *Table) advanceToNextPlayer() {
	if t.game == nil {
		return
	}
	t.game.advanceToNextPlayer()
}

// syncPlayerState synchronizes the player state from game layer to table layer using Rob Pike's pattern
func (t *Table) syncPlayerState(userID string) error {
	user := t.users[userID]
	if user == nil {
		return fmt.Errorf("user not found: %s", userID)
	}

	// Find the corresponding player in the game
	for _, player := range t.game.players {
		if player.ID == userID {
			// Update basic fields from player to user
			user.HasBet = player.HasBet
			user.AccountBalance = player.Balance
			user.LastAction = player.LastAction
			user.HasFolded = player.HasFolded
			user.Hand = player.Hand

			// Use Rob Pike's pattern - dispatch to let state function decide transitions
			if player.stateMachine != nil {
				// Create a callback to sync user state when player state transitions
				callback := func(stateName string, event statemachine.StateEvent) {
					if event == statemachine.StateEntered {
						user.SetGameState(stateName)
					}
				}

				// Dispatch - the player's current state function will examine conditions
				// and return the appropriate next state function
				player.stateMachine.Dispatch(callback)
			}
			return nil
		}
	}

	return fmt.Errorf("player not found in game: %s", userID)
}

// initializeCurrentPlayer delegates to Game layer
func (t *Table) initializeCurrentPlayer() {
	if t.game == nil {
		return
	}
	t.game.initializeCurrentPlayer()
}

// HandleFold handles folding by delegating to the Game layer
func (t *Table) HandleFold(userID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	user := t.users[userID]
	if user == nil {
		return fmt.Errorf("user not found")
	}

	// Validate that it's this player's turn to act
	if t.isGameActive() && t.game != nil {
		currentPlayerID := t.currentPlayerID()
		t.log.Debugf("HandleFold: userID=%s, currentPlayerID=%s, currentPlayer=%d, gamePhase=%v",
			userID, currentPlayerID, t.game.currentPlayer, t.game.phase)
		if currentPlayerID != userID {
			return fmt.Errorf("not your turn to act")
		}

		// Delegate to Game layer - this handles all the folding logic
		err := t.game.handlePlayerFold(userID)
		if err != nil {
			return err
		}

		// Sync the updated player state back to user using state machine dispatch
		if err := t.syncPlayerState(userID); err != nil {
			t.log.Errorf("Failed to sync player state after fold: %v", err)
		}

		t.log.Debugf("HandleFold: User %s folded, HasFolded=%t", userID, user.HasFolded)

		// Check if this action completes the betting round
		t.maybeAdvancePhase()
	}

	// SINGLE broadcast at the end of all processing
	t.broadcastGameStateUpdate()

	t.lastAction = time.Now()
	return nil
}

// HandleCall handles call actions by delegating to the Game layer
func (t *Table) HandleCall(userID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	user := t.users[userID]
	if user == nil {
		return fmt.Errorf("user not found")
	}

	// Validate that it's this player's turn to act
	if t.isGameActive() && t.game != nil {
		currentPlayerID := t.currentPlayerID()
		t.log.Debugf("HandleCall: userID=%s, currentPlayerID=%s, currentPlayer=%d, gamePhase=%v",
			userID, currentPlayerID, t.game.currentPlayer, t.game.phase)
		if currentPlayerID != userID {
			return fmt.Errorf("not your turn to act")
		}

		// Delegate to Game layer - this handles all the calling logic
		err := t.game.handlePlayerCall(userID)
		if err != nil {
			return err
		}

		// Sync the updated player state back to user using state machine dispatch
		if err := t.syncPlayerState(userID); err != nil {
			t.log.Errorf("Failed to sync player state after call: %v", err)
		}

		t.log.Debugf("HandleCall: user %s called, actionsInRound=%d, advancing to next player", userID, t.game.GetActionsInRound())

		// Check if this action completes the betting round
		t.maybeAdvancePhase()
	}

	// SINGLE broadcast at the end of all processing
	t.broadcastGameStateUpdate()

	t.lastAction = time.Now()
	return nil
}

// HandleCheck handles check actions by delegating to the Game layer
func (t *Table) HandleCheck(userID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	user := t.users[userID]
	if user == nil {
		return fmt.Errorf("user not found")
	}

	// Validate that it's this player's turn to act
	if t.isGameActive() && t.game != nil {
		currentPlayerID := t.currentPlayerID()
		t.log.Debugf("HandleCheck: userID=%s, currentPlayerID=%s, currentPlayer=%d, gamePhase=%v",
			userID, currentPlayerID, t.game.currentPlayer, t.game.phase)
		if currentPlayerID != userID {
			return fmt.Errorf("not your turn to act")
		}

		// Delegate to Game layer - this handles all the checking logic
		err := t.game.handlePlayerCheck(userID)
		if err != nil {
			return err
		}

		// Sync the updated player state back to user using state machine dispatch
		if err := t.syncPlayerState(userID); err != nil {
			t.log.Errorf("Failed to sync player state after check: %v", err)
		}

		t.log.Debugf("HandleCheck: user %s checking, bet=%d, currentBet=%d", userID, user.HasBet, t.game.currentBet)
		t.log.Debugf("HandleCheck: incremented actionsInRound to %d", t.game.GetActionsInRound())

		// Check if this action completes the betting round
		t.maybeAdvancePhase()
	}

	// SINGLE broadcast at the end of all processing
	t.broadcastGameStateUpdate()

	t.lastAction = time.Now()
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
		if smallBlindAmount > t.game.players[smallBlindPos].Balance {
			return fmt.Errorf("insufficient balance for small blind")
		}
		t.game.players[smallBlindPos].Balance -= smallBlindAmount
		t.game.players[smallBlindPos].HasBet = smallBlindAmount
		t.game.potManager.AddBet(smallBlindPos, smallBlindAmount)

		// Send small blind notification
		go t.eventManager.NotifyBlindPosted(t.config.ID, t.game.players[smallBlindPos].ID, smallBlindAmount, true)
	}

	// Post big blind
	if t.game.players[bigBlindPos] != nil {
		bigBlindAmount := t.game.config.BigBlind
		if bigBlindAmount > t.game.players[bigBlindPos].Balance {
			return fmt.Errorf("insufficient balance for big blind")
		}
		t.game.players[bigBlindPos].Balance -= bigBlindAmount
		t.game.players[bigBlindPos].HasBet = bigBlindAmount
		t.game.potManager.AddBet(bigBlindPos, bigBlindAmount)
		t.game.currentBet = bigBlindAmount // Set current bet to big blind amount

		// Send big blind notification
		go t.eventManager.NotifyBlindPosted(t.config.ID, t.game.players[bigBlindPos].ID, bigBlindAmount, false)
	}

	return nil
}

// StartNewHand starts a new hand without requiring all players to be ready again (public API)
func (t *Table) StartNewHand() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.startNewHand()
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
			u.Hand = append(u.Hand, card)

			// Also sync the card to the corresponding game player
			found := false
			for _, player := range t.game.players {
				if player.ID == u.ID {
					player.Hand = append(player.Hand, card)
					found = true
					break
				}
			}

			if !found {
				t.log.Debugf("DEBUG: Could not find game player for user %s when dealing cards", u.ID)
			} else {
				// Find the correct game player for hand size debug
				var gamePlayerHandSize int
				for _, player := range t.game.players {
					if player.ID == u.ID {
						gamePlayerHandSize = len(player.Hand)
						break
					}
				}
				t.log.Debugf("DEBUG: Dealt card %s to player %s (user hand: %d, game player hand: %d)",
					card.String(), u.ID, len(u.Hand), gamePlayerHandSize)
			}
		}
	}
	return nil
}

// SetNotificationSender sets the notification sender for the table
func (t *Table) SetNotificationSender(sender NotificationSender) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.eventManager.notificationSender = sender
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
func (t *Table) AddNewUser(id, name string, accountBalance int64, seat int) (*User, error) {
	user := NewUser(id, name, accountBalance, seat)
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

// GetUser returns a user by ID
func (t *Table) GetUser(userID string) *User {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.users[userID]
}

// TriggerPlayerReadyEvent sends a player ready notification immediately
func (t *Table) TriggerPlayerReadyEvent(userID string, ready bool) {
	// Send notification immediately
	go t.eventManager.NotifyPlayerReady(t.config.ID, userID, ready)
}

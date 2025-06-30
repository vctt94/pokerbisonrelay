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
	ID                string
	Name              string
	DCRAccountBalance int64 // DCR account balance (in atoms)
	TableSeat         int   // Seat position at the table
	IsReady           bool  // Ready to start/continue games
	JoinedAt          time.Time
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

// StateSaver is an interface for saving table state
type StateSaver interface {
	SaveTableStateAsync(tableID string, reason string)
}

// TableEventManager handles notifications and state updates for table events
type TableEventManager struct {
	notificationSender NotificationSender
	stateSaver         StateSaver
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
		// Removed automatic BroadcastGameStateUpdate to prevent infinite loops during active gameplay
		// The caller should decide when to broadcast if needed
	}
}

// NotifyAllPlayersReady sends all players ready notification
func (tem *TableEventManager) NotifyAllPlayersReady(tableID string) {
	if tem.notificationSender != nil {
		tem.notificationSender.SendAllPlayersReady(tableID)
		// Removed automatic BroadcastGameStateUpdate to prevent infinite loops during active gameplay
		// The caller should decide when to broadcast if needed
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

// SaveState triggers a state save for the table
func (tem *TableEventManager) SaveState(tableID string, reason string) {
	if tem.stateSaver != nil {
		tem.stateSaver.SaveTableStateAsync(tableID, reason)
	}
}

// SetStateSaver sets the state saver for the event manager
func (tem *TableEventManager) SetStateSaver(stateSaver StateSaver) {
	tem.stateSaver = stateSaver
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
			// Save state when players become ready
			entity.eventManager.SaveState(entity.config.ID, "players ready")
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
	if callback != nil {
		callback("PLAYERS_READY", statemachine.StateEntered)
	}
	// Save state when entering players ready
	entity.eventManager.SaveState(entity.config.ID, "players ready state")
	// This state waits for external trigger (StartGame)
	return tableStatePlayersReady
}

// tableStateGameActive handles the GAME_ACTIVE state logic
func tableStateGameActive(entity *Table, callback func(stateName string, event statemachine.StateEvent)) TableStateFn {
	if callback != nil {
		callback("GAME_ACTIVE", statemachine.StateEntered)
	}
	// Save state when game is active
	entity.eventManager.SaveState(entity.config.ID, "game active")
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
	t.stateMachine.Dispatch(nil)

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

	// Create a new game - players are managed by the table
	g, err := NewGame(GameConfig{
		NumPlayers:     len(activePlayers),
		StartingChips:  t.config.StartingChips,
		SmallBlind:     t.config.SmallBlind,
		BigBlind:       t.config.BigBlind,
		AutoStartDelay: t.config.AutoStartDelay,
		Log:            t.log,
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
	})

	// Set the players in the game to reference the same objects from the table
	t.game.SetPlayers(activePlayers)

	// Use centralized hand setup logic (this assumes lock is held)
	err = t.setupNewHand(activePlayers)
	if err != nil {
		return err
	}

	// Transition to game active state with broadcast callback
	t.stateMachine.SetState(tableStateGameActive)
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
	_ = t.game.handleShowdown()

	// Count players still at the table with sufficient balance for the next hand
	playersReadyForNextHand := 0
	for _, u := range t.users {
		if u.DCRAccountBalance >= t.config.BigBlind {
			playersReadyForNextHand++
		}
	}

	t.game.ResetActionsInRound()
	t.lastAction = time.Now()

	// Auto-start functionality is now handled by the Game layer in handleShowdown
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
		if u.DCRAccountBalance >= t.config.BigBlind {
			activeUsers = append(activeUsers, u)
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
			if player.ID == user.ID {
				existingPlayer = player
				break
			}
		}

		if existingPlayer != nil {
			// Reset the existing player for the new hand, preserving their current balance
			existingPlayer.ResetForNewHand(existingPlayer.Balance)
			activePlayers = append(activePlayers, existingPlayer)
		} else {
			// This is a new player that joined between hands - create a new Player object
			newPlayer := NewPlayer(user.ID, user.Name, t.config.StartingChips)
			newPlayer.TableSeat = user.TableSeat
			newPlayer.IsReady = user.IsReady
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

	t.lastAction = time.Now()
	return nil
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

		// Check if this action completes the betting round
		t.maybeAdvancePhase()
	}

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

}

// GetGame returns the current game (can be nil)
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

		// Check if this action completes the betting round
		t.maybeAdvancePhase()
	}

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

		t.log.Debugf("HandleCall: user %s called, actionsInRound=%d, advancing to next player", userID, t.game.GetActionsInRound())

		// Check if this action completes the betting round
		t.maybeAdvancePhase()
	}

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

		t.log.Debugf("HandleCheck: incremented actionsInRound to %d", t.game.GetActionsInRound())

		// Check if this action completes the betting round
		t.maybeAdvancePhase()
	}

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
		// DISABLED: Notification callbacks cause deadlocks - server handles notifications directly
		// go t.eventManager.NotifyBlindPosted(t.config.ID, t.game.players[smallBlindPos].ID, smallBlindAmount, true)
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
				if player.ID == u.ID {
					player.Hand = append(player.Hand, card)
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

// SetNotificationSender sets the notification sender for the table
func (t *Table) SetNotificationSender(sender NotificationSender) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.eventManager.notificationSender = sender
}

// SetStateSaver sets the state saver for the table
func (t *Table) SetStateSaver(saver StateSaver) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.eventManager.SetStateSaver(saver)
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
	return t.game.phase
}

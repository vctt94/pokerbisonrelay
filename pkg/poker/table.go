package poker

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/decred/slog"

	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
)

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

// Table represents a poker table
type Table struct {
	log             slog.Logger
	config          TableConfig
	players         map[string]*Player
	game            *Game // Game logic without separate player management
	mu              sync.RWMutex
	createdAt       time.Time
	lastAction      time.Time
	gameStarted     bool
	allPlayersReady bool
	// Track actions in current betting round
	actionsInRound int
	// Event manager for notifications
	eventManager *TableEventManager
	// Auto-start management
	autoStartTimer    *time.Timer
	autoStartCanceled bool
}

// NewTable creates a new poker table
func NewTable(cfg TableConfig) *Table {
	return &Table{
		log:            cfg.Log,
		config:         cfg,
		players:        make(map[string]*Player),
		createdAt:      time.Now(),
		lastAction:     time.Now(),
		actionsInRound: 0,
		eventManager:   &TableEventManager{},
	}
}

// SetNotificationSender sets the notification sender for the table
func (t *Table) SetNotificationSender(sender NotificationSender) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.eventManager.notificationSender = sender
}

// AddPlayer adds a player to the table with the specified starting chips
// startingChips: the amount of poker chips the player starts with (DCR buy-in validation done by caller)
func (t *Table) AddPlayer(playerID string, startingChips int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check if table is full
	if len(t.players) >= t.config.MaxPlayers {
		return fmt.Errorf("table is full")
	}

	// Check if player already at table
	if _, exists := t.players[playerID]; exists {
		return fmt.Errorf("player already at table")
	}

	// Add player to table with unified state
	player := &Player{
		ID:              playerID,
		Balance:         startingChips, // In-game chips for current/next hand
		StartingBalance: startingChips,
		AccountBalance:  0, // Not tracking DCR balance at table level
		TableSeat:       len(t.players),
		IsReady:         false,
		HasFolded:       false,
		IsAllIn:         false,
		LastAction:      time.Now(),
	}

	// Initialize player with at-table state
	player.transitionTo(playerStateAtTable)
	t.players[playerID] = player

	t.lastAction = time.Now()
	return nil
}

// RemovePlayer removes a player from the table
func (t *Table) RemovePlayer(playerID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.players[playerID]; !exists {
		return fmt.Errorf("player not at table")
	}

	delete(t.players, playerID)
	t.lastAction = time.Now()
	return nil
}

// CheckAllPlayersReady checks if all players are ready
func (t *Table) CheckAllPlayersReady() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if len(t.players) < t.config.MinPlayers {
		return false
	}

	for _, p := range t.players {
		if !p.IsReady {
			return false
		}
	}

	// If all players are ready and this is the first time, send notification
	wasReady := t.allPlayersReady
	t.allPlayersReady = true

	if !wasReady {
		// Send ALL_PLAYERS_READY notification immediately
		go t.eventManager.NotifyAllPlayersReady(t.config.ID)
	}

	return true
}

// TriggerPlayerReadyEvent sends a player ready notification immediately
func (t *Table) TriggerPlayerReadyEvent(playerID string, ready bool) {
	// Send notification immediately
	go t.eventManager.NotifyPlayerReady(t.config.ID, playerID, ready)
}

// GetPlayer returns a player by ID
func (t *Table) GetPlayer(playerID string) *Player {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.players[playerID]
}

// StartGame starts a new game at the table
func (t *Table) StartGame() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Cancel any pending auto-start since we're manually starting
	t.cancelAutoStart()

	// Check if we have enough players
	if len(t.players) < t.config.MinPlayers {
		return fmt.Errorf("not enough players to start game")
	}

	// Reset all players for the new hand
	activePlayers := make([]*Player, 0, len(t.players))
	for _, p := range t.players {
		if p.IsAtTable() { // Only include players still at the table
			p.ResetForNewHand(t.config.StartingChips)
			activePlayers = append(activePlayers, p)
		}
	}

	// Sort players by TableSeat for consistent ordering
	sort.Slice(activePlayers, func(i, j int) bool {
		return activePlayers[i].TableSeat < activePlayers[j].TableSeat
	})

	// Create a new game
	t.game = NewGame(GameConfig{
		NumPlayers:    len(activePlayers),
		StartingChips: t.config.StartingChips,
		SmallBlind:    t.config.SmallBlind,
		BigBlind:      t.config.BigBlind,
	})
	// Populate game.players with references to the same Player objects from the table
	copy(t.game.players, activePlayers)

	// Use centralized hand setup logic (this assumes lock is held)
	err := t.setupNewHand(activePlayers)
	if err != nil {
		return err
	}

	// Mark game as started
	t.gameStarted = true

	t.lastAction = time.Now()
	return nil
}

// setupNewHand handles the complete setup process for a new hand (assumes lock is held)
func (t *Table) setupNewHand(activePlayers []*Player) error {
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

	// Phase 4: Set up timing and player states
	t.log.Debugf("setupNewHand: Phase 4 - Setting up timing and player states")
	for _, p := range activePlayers {
		p.LastAction = time.Now()
	}

	// Phase 5: Setup complete notification
	t.log.Debugf("setupNewHand: Phase 5 - Setup complete, ready for notification sequence")
	t.log.Debugf("setupNewHand: Hand setup completed successfully")

	return nil
}

// GetStatus returns the current status of the table
func (t *Table) GetStatus() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	status := fmt.Sprintf("Table %s:\n", t.config.ID)
	status += fmt.Sprintf("Players: %d/%d\n", len(t.players), t.config.MaxPlayers)
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

// GetPlayers returns all players at the table
func (t *Table) GetPlayers() []*Player {
	t.mu.RLock()
	defer t.mu.RUnlock()

	players := make([]*Player, 0, len(t.players))
	for _, p := range t.players {
		players = append(players, p)
	}

	// Sort by TableSeat to ensure consistent ordering
	sort.Slice(players, func(i, j int) bool {
		return players[i].TableSeat < players[j].TableSeat
	})

	return players
}

// GetBigBlind returns the big blind value for the table
func (t *Table) GetBigBlind() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.config.BigBlind
}

// MakeBet handles betting using the unified player state system
func (t *Table) MakeBet(playerID string, amount int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	player := t.players[playerID]
	if player == nil {
		return fmt.Errorf("player not found")
	}

	// Validate that it's this player's turn to act
	if t.gameStarted && t.game != nil {
		currentPlayerID := t.currentPlayerID()
		t.log.Debugf("MakeBet: playerID=%s, currentPlayerID=%s, currentPlayer=%d, gamePhase=%v, amount=%d",
			playerID, currentPlayerID, t.game.currentPlayer, t.game.phase, amount)
		if currentPlayerID != playerID {
			return fmt.Errorf("not your turn to act")
		}
	}

	// Validate and make the bet
	if amount < player.HasBet {
		return fmt.Errorf("cannot decrease bet")
	}

	delta := amount - player.HasBet
	if delta > 0 && delta > player.Balance {
		return fmt.Errorf("insufficient balance")
	}

	// Update the shared player object (this updates both table and game state automatically)
	if delta > 0 {
		player.Balance -= delta
	}
	player.HasBet = amount
	player.LastAction = time.Now()

	// Update game state
	if player.Balance == 0 {
		player.SetGameState("ALL_IN")
	}

	// Update game-level state
	if t.gameStarted && t.game != nil {
		if amount > t.game.currentBet {
			t.game.currentBet = amount
		}
		if delta > 0 {
			// Find player index in game players slice
			playerIndex := -1
			for i, p := range t.game.players {
				if p.ID == playerID {
					playerIndex = i
					break
				}
			}
			if playerIndex >= 0 {
				t.game.AddToPotForPlayer(playerIndex, delta)
			}
		}
		// Increment actions counter for this betting round
		t.actionsInRound++
		// Advance to next player after action
		t.advanceToNextPlayer()
	}

	// Possibly advance phase if betting round is complete
	t.maybeAdvancePhase()

	// Broadcast game state update to notify clients of the current player change
	go t.eventManager.notificationSender.BroadcastGameStateUpdate(t.config.ID)

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

// IsGameStarted returns whether the game has started
func (t *Table) IsGameStarted() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.gameStarted
}

// AreAllPlayersReady returns whether all players are ready
func (t *Table) AreAllPlayersReady() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.allPlayersReady
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
	if !t.gameStarted || t.config.TimeBank == 0 || t.game == nil {
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
			t.actionsInRound++

			// Advance to next player after check action
			t.advanceToNextPlayer()
		} else {
			// Auto-fold the current player
			currentPlayer.HasFolded = true

			// Advance to next player
			t.advanceToNextPlayer()
		}

		// Check if this action completes the betting round
		t.maybeAdvancePhase()
	}
}

// maybeAdvancePhase checks if betting round is finished and progresses the game phase.
func (t *Table) maybeAdvancePhase() {
	if !t.gameStarted || t.game == nil {
		return
	}

	// Don't advance during NEW_HAND_DEALING phase - this is managed by setupNewHandLocked()
	// which handles the complete setup sequence and phase transitions internally
	if t.game.phase == pokerrpc.GamePhase_NEW_HAND_DEALING {
		return
	}

	// Count active players (non-folded)
	activePlayers := 0
	for _, p := range t.players {
		if !p.HasFolded {
			activePlayers++
		}
	}

	t.log.Debugf("maybeAdvancePhase: phase=%v, activePlayers=%d, actionsInRound=%d",
		t.game.phase, activePlayers, t.actionsInRound)

	// If zero or one active player, move to showdown
	if activePlayers <= 1 {
		t.log.Debugf("maybeAdvancePhase: Moving to showdown with %d active players", activePlayers)
		t.game.phase = pokerrpc.GamePhase_SHOWDOWN
		t.handleShowdown()
		return
	}

	// Check if all active players have had a chance to act and all bets are equal
	// A betting round is complete when:
	// 1. At least each active player has had one action (actionsInRound >= activePlayers)
	// 2. All active players have matching bets (or have folded)

	if t.actionsInRound < activePlayers {
		return // Not all players have acted yet
	}

	// Check if all active players have matching bets
	currentBet := t.game.currentBet
	for _, p := range t.players {
		if p.HasFolded {
			continue
		}
		if p.HasBet != currentBet {
			return // Still players with unmatched bets
		}
	}

	// Betting round is complete - advance to next phase
	switch t.game.phase {
	case pokerrpc.GamePhase_PRE_FLOP:
		t.game.StateFlop()
	case pokerrpc.GamePhase_FLOP:
		t.game.StateTurn()
	case pokerrpc.GamePhase_TURN:
		t.game.StateRiver()
	case pokerrpc.GamePhase_RIVER:
		t.game.phase = pokerrpc.GamePhase_SHOWDOWN
		t.handleShowdown()
		return
	}

	// Reset for new betting round
	for _, p := range t.players {
		p.HasBet = 0
	}
	t.game.currentBet = 0
	t.actionsInRound = 0 // Reset actions counter for new betting round

	// Reset current player for new betting round
	t.initializeCurrentPlayer()

	// Set the new current player's LastAction to now for the new betting round
	if t.game.currentPlayer >= 0 && t.game.currentPlayer < len(t.game.players) {
		if !t.game.players[t.game.currentPlayer].HasFolded {
			t.game.players[t.game.currentPlayer].LastAction = time.Now()
		}
	}

	// Broadcast game state update to notify clients of the phase change
	go t.eventManager.notificationSender.BroadcastGameStateUpdate(t.config.ID)
}

// MaybeAdvancePhase is an exported wrapper that allows external callers
// (such as the gRPC server) to trigger a check of whether the betting
// round has finished and the game should progress to the next phase.
// It simply delegates to the unexported maybeAdvancePhase method while
// ensuring proper locking semantics within the Table.
func (t *Table) MaybeAdvancePhase() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.maybeAdvancePhase()
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

// advanceToNextPlayer moves to the next active player (assumes lock is held)
func (t *Table) advanceToNextPlayer() {
	if t.game == nil || len(t.game.players) == 0 {
		return
	}

	// Find next active player (who hasn't folded)
	startingPlayer := t.game.currentPlayer
	for {
		t.game.currentPlayer = (t.game.currentPlayer + 1) % len(t.game.players)

		// Check if we've gone full circle without finding an active player
		if t.game.currentPlayer == startingPlayer {
			break
		}

		// Use the unified player state directly
		if !t.game.players[t.game.currentPlayer].HasFolded {
			// Set the new current player's LastAction to now so their timeout timer starts
			t.game.players[t.game.currentPlayer].LastAction = time.Now()
			break
		}
	}
}

// initializeCurrentPlayer is the internal implementation that assumes the lock is already held
func (t *Table) initializeCurrentPlayer() {
	if t.game == nil || len(t.game.players) == 0 {
		return
	}

	numPlayers := len(t.game.players)

	t.log.Debugf("initializeCurrentPlayer: numPlayers=%d, dealer=%d, phase=%v",
		numPlayers, t.game.dealer, t.game.phase)

	// In pre-flop, start with Under the Gun (player after big blind)
	if t.game.phase == pokerrpc.GamePhase_PRE_FLOP {
		if numPlayers == 2 {
			// In heads-up, after blinds are posted, small blind acts first
			// The small blind IS the dealer in heads-up
			t.game.currentPlayer = t.game.dealer
			t.log.Debugf("initializeCurrentPlayer: heads-up pre-flop, setting currentPlayer to dealer=%d", t.game.dealer)
		} else {
			// In multi-way, Under the Gun acts first (after big blind)
			t.game.currentPlayer = (t.game.dealer + 3) % numPlayers
			t.log.Debugf("initializeCurrentPlayer: multi-way pre-flop, setting currentPlayer to UTG=%d", t.game.currentPlayer)
		}
	} else {
		// In post-flop streets, start with small blind position
		if numPlayers == 2 {
			// In heads-up, small blind is the dealer
			t.game.currentPlayer = t.game.dealer
			t.log.Debugf("initializeCurrentPlayer: heads-up post-flop, setting currentPlayer to dealer=%d", t.game.dealer)
		} else {
			// In multi-way, small blind is player after dealer
			t.game.currentPlayer = (t.game.dealer + 1) % numPlayers
			t.log.Debugf("initializeCurrentPlayer: multi-way post-flop, setting currentPlayer to SB=%d", t.game.currentPlayer)
		}
	}

	// Ensure we start with an active player
	startingPlayer := t.game.currentPlayer
	for {
		// Use the unified player state directly
		if !t.game.players[t.game.currentPlayer].HasFolded {
			break
		}

		t.game.currentPlayer = (t.game.currentPlayer + 1) % len(t.game.players)

		// Prevent infinite loop
		if t.game.currentPlayer == startingPlayer {
			break
		}
	}
}

// HandleFold handles folding using the unified player state system
func (t *Table) HandleFold(playerID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	player := t.players[playerID]
	if player == nil {
		return fmt.Errorf("player not found")
	}

	// Validate that it's this player's turn to act
	if t.gameStarted && t.game != nil {
		currentPlayerID := t.currentPlayerID()
		t.log.Debugf("HandleFold: playerID=%s, currentPlayerID=%s, currentPlayer=%d, gamePhase=%v",
			playerID, currentPlayerID, t.game.currentPlayer, t.game.phase)
		if currentPlayerID != playerID {
			return fmt.Errorf("not your turn to act")
		}
	}

	// Update the shared player object (this updates both table and game state automatically)
	player.SetGameState("FOLDED")
	player.LastAction = time.Now()

	t.log.Debugf("HandleFold: Player %s folded, HasFolded=%t", playerID, player.HasFolded)

	// Update game state
	if t.gameStarted && t.game != nil {
		// Increment actions counter for this betting round
		t.actionsInRound++
		// Advance to next player after fold action
		t.advanceToNextPlayer()
	}

	// Possibly advance phase if betting round is complete
	t.maybeAdvancePhase()

	// Broadcast game state update to notify clients of the current player change
	go t.eventManager.notificationSender.BroadcastGameStateUpdate(t.config.ID)

	t.lastAction = time.Now()
	return nil
}

// HandleCall handles call actions using the unified player state system
func (t *Table) HandleCall(playerID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	player := t.players[playerID]
	if player == nil {
		return fmt.Errorf("player not found")
	}

	// Validate that it's this player's turn to act
	if t.gameStarted && t.game != nil {
		currentPlayerID := t.currentPlayerID()
		t.log.Debugf("HandleCall: playerID=%s, currentPlayerID=%s, currentPlayer=%d, gamePhase=%v",
			playerID, currentPlayerID, t.game.currentPlayer, t.game.phase)
		if currentPlayerID != playerID {
			return fmt.Errorf("not your turn to act")
		}
	}

	currentBet := t.game.currentBet

	// For call to be valid, there must be a bet to call (current bet > player's current bet)
	if currentBet <= player.HasBet {
		return fmt.Errorf("nothing to call - use check instead")
	}

	// Calculate how much more the player needs to bet to call
	delta := currentBet - player.HasBet
	if delta > player.Balance {
		return fmt.Errorf("insufficient balance to call")
	}

	// Update the shared player object (this updates both table and game state automatically)
	player.Balance -= delta
	player.HasBet = currentBet
	player.LastAction = time.Now()

	// Update game state
	if player.Balance == 0 {
		player.SetGameState("ALL_IN")
	}

	// Update game-level state
	if delta > 0 {
		// Find player index in game players slice
		playerIndex := -1
		for i, p := range t.game.players {
			if p.ID == playerID {
				playerIndex = i
				break
			}
		}
		if playerIndex >= 0 {
			t.game.AddToPotForPlayer(playerIndex, delta)
		}
	}

	// Increment actions counter for this betting round
	t.actionsInRound++

	// Advance to next player after call action
	t.advanceToNextPlayer()

	// Check if this action completes the betting round
	t.maybeAdvancePhase()

	// Broadcast game state update to notify clients of the current player change
	if t.eventManager.notificationSender != nil {
		go t.eventManager.notificationSender.BroadcastGameStateUpdate(t.config.ID)
	}

	t.lastAction = time.Now()
	return nil
}

// HandleCheck handles check actions using the unified player state system
func (t *Table) HandleCheck(playerID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	player := t.players[playerID]
	if player == nil {
		return fmt.Errorf("player not found")
	}

	// Validate that it's this player's turn to act
	if t.gameStarted && t.game != nil {
		currentPlayerID := t.currentPlayerID()
		t.log.Debugf("HandleCheck: playerID=%s, currentPlayerID=%s, currentPlayer=%d, gamePhase=%v",
			playerID, currentPlayerID, t.game.currentPlayer, t.game.phase)
		if currentPlayerID != playerID {
			return fmt.Errorf("not your turn to act")
		}
	}

	// Check action - player can only check if their current bet equals the table's current bet
	currentBet := t.game.currentBet

	if player.HasBet < currentBet {
		return fmt.Errorf("cannot check when there's a bet to call (player bet: %d, current bet: %d)",
			player.HasBet, currentBet)
	}

	// Update player's last action time
	player.LastAction = time.Now()

	// Update game state
	if t.gameStarted && t.game != nil {
		// Increment actions counter for this betting round
		t.actionsInRound++
		// Advance to next player after check action
		t.advanceToNextPlayer()
	}

	// Possibly advance phase if betting round is complete
	t.maybeAdvancePhase()

	// Broadcast game state update to notify clients of the current player change
	if t.eventManager.notificationSender != nil {
		go t.eventManager.notificationSender.BroadcastGameStateUpdate(t.config.ID)
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

// handleShowdown handles the showdown phase, distributes pots, and prepares for a new hand
func (t *Table) handleShowdown() {
	if t.game == nil {
		return
	}

	// Count active (non-folded) players
	activePlayers := make([]*Player, 0)
	for _, player := range t.game.players {
		if !player.HasFolded {
			activePlayers = append(activePlayers, player)
		}
	}

	// Track winners before distributing pots
	t.game.winners = make([]string, 0)
	var winnersForNotification []*pokerrpc.Winner

	// Store total pot for notification (before any pot distribution)
	totalPotForNotification := t.game.GetPot()

	// If only one player remains, they win automatically without hand evaluation
	if len(activePlayers) <= 1 {
		// Award the pot to the remaining player (if any)
		if len(activePlayers) == 1 {
			winnings := t.game.GetPot()
			activePlayers[0].Balance += winnings
			t.game.winners = append(t.game.winners, activePlayers[0].ID)

			// Create winner notification with their cards
			winnersForNotification = append(winnersForNotification, &pokerrpc.Winner{
				PlayerId: activePlayers[0].ID,
				Winnings: winnings,
				BestHand: CreateHandFromCards(activePlayers[0].Hand),
			})
		}
	} else {
		// Multiple players remain - need proper hand evaluation
		// Only evaluate hands if we have enough cards (player hand + community cards >= 5)
		validEvaluations := true

		for _, player := range activePlayers {
			totalCards := len(player.Hand) + len(t.game.communityCards)
			if totalCards < 5 {
				validEvaluations = false
				break
			}
		}

		if validEvaluations {
			// Evaluate each active player's hand
			for _, player := range activePlayers {
				handValue := EvaluateHand(player.Hand, t.game.communityCards)
				player.HandValue = &handValue
				player.HandDescription = GetHandDescription(handValue)
			}

			// Check for any uncalled bets and return them
			t.game.potManager.ReturnUncalledBet(t.game.players)

			// Create side pots if needed
			t.game.potManager.CreateSidePots(t.game.players)

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

			// Store total pot before distribution (since DistributePots empties the pots)
			totalPot := t.game.GetPot()

			// Distribute pots to winners
			t.game.potManager.DistributePots(t.game.players)

			// Create winner notifications with proper hand information
			winningsPerPlayer := totalPot / int64(len(bestPlayers))

			for _, winner := range bestPlayers {
				t.game.winners = append(t.game.winners, winner.ID)

				winnersForNotification = append(winnersForNotification, &pokerrpc.Winner{
					PlayerId: winner.ID,
					HandRank: winner.HandValue.HandRank,
					BestHand: CreateHandFromCards(winner.HandValue.BestHand),
					Winnings: winningsPerPlayer,
				})
			}
		} else {
			// Can't properly evaluate hands - award pot to first active player
			// This is a fallback for incomplete games
			if len(activePlayers) > 0 {
				winnings := t.game.GetPot()
				activePlayers[0].Balance += winnings
				t.game.winners = append(t.game.winners, activePlayers[0].ID)

				winnersForNotification = append(winnersForNotification, &pokerrpc.Winner{
					PlayerId: activePlayers[0].ID,
					Winnings: winnings,
					BestHand: CreateHandFromCards(activePlayers[0].Hand),
				})
			}
		}
	}

	// Send showdown result notification to all players
	if t.eventManager.notificationSender != nil && len(winnersForNotification) > 0 {
		t.eventManager.notificationSender.SendShowdownResult(t.config.ID, winnersForNotification, totalPotForNotification)
		t.eventManager.notificationSender.BroadcastGameStateUpdate(t.config.ID)
	}

	// Since we're using unified player objects, balances are already updated
	// No need to synchronize - the game.players ARE the table.players

	// Count players still at the table with sufficient balance for the next hand
	playersReadyForNextHand := 0
	for _, p := range t.players {
		if p.IsAtTable() && p.Balance >= t.config.BigBlind {
			playersReadyForNextHand++
		}
	}

	// Don't reset hand-specific state here - keep hands visible during showdown
	// Only reset betting-related state
	for _, p := range t.players {
		p.HasFolded = false
		p.HasBet = 0
		// Keep p.Hand, p.HandValue, p.HandDescription for showdown viewing
	}

	t.actionsInRound = 0
	t.lastAction = time.Now()

	// Leave the game in SHOWDOWN phase so clients can check results
	// Auto-start next hand after configured delay if enough players remain
	if playersReadyForNextHand >= t.config.MinPlayers && t.config.AutoStartDelay > 0 {
		t.scheduleAutoStart()
	}
}

// StartNewHand starts a new hand without requiring all players to be ready again (public API)
func (t *Table) StartNewHand() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.startNewHand()
}

// startNewHand is the internal implementation that assumes the lock is already held
func (t *Table) startNewHand() error {
	// Ensure game exists - if not, this is a bug
	if t.game == nil {
		return fmt.Errorf("startNewHand called but game is nil - this should not happen")
	}

	// Check if enough players still at table
	playersAtTable := 0
	for _, p := range t.players {
		if p.IsAtTable() {
			playersAtTable++
		}
	}

	if playersAtTable < t.config.MinPlayers {
		return fmt.Errorf("not enough players to start new hand")
	}

	// Get active players for the new hand
	activePlayers := make([]*Player, 0, len(t.players))
	for _, p := range t.players {
		if p.IsAtTable() && p.Balance >= t.config.BigBlind {
			activePlayers = append(activePlayers, p)
		}
	}

	// Sort players by TableSeat for consistent ordering
	sort.Slice(activePlayers, func(i, j int) bool {
		return activePlayers[i].TableSeat < activePlayers[j].TableSeat
	})

	// Reset all players' hand-specific state from previous hand
	for _, p := range t.players {
		if p.IsAtTable() {
			oldHandSize := len(p.Hand)
			p.ResetForNewHand(p.Balance) // Use consistent reset method
			// Additional cleanup that ResetForNewHand doesn't handle
			p.IsAllIn = false
			p.IsTurn = false
			p.IsDealer = false
			t.log.Debugf("startNewHand: reset player %s - old hand size: %d, new hand size: %d", p.ID, oldHandSize, len(p.Hand))
		}
	}

	// Reset existing game for new hand
	t.game.ResetForNewHand(activePlayers)

	// Use centralized hand setup logic (this assumes lock is held)
	err := t.setupNewHand(activePlayers)
	if err != nil {
		return err
	}

	// Send NEW_HAND_STARTED notification FIRST (synchronously) so clients can clear old state
	t.log.Debugf("startNewHand: sending NEW_HAND_STARTED notification")
	if t.eventManager.notificationSender != nil {
		t.eventManager.notificationSender.SendNewHandStarted(t.config.ID)
	}

	// Then broadcast the complete game state AFTER notification is sent
	// Add a small delay to ensure setup is completely finished before broadcast
	t.log.Debugf("startNewHand: broadcasting complete game state")
	if t.eventManager.notificationSender != nil {
		go func() {
			time.Sleep(10 * time.Millisecond) // Small delay to ensure setup completion
			t.eventManager.notificationSender.BroadcastGameStateUpdate(t.config.ID)
		}()
	}

	return nil
}

// dealCardsToPlayers deals cards to active players using the unified player state
func (t *Table) dealCardsToPlayers(activePlayers []*Player) error {
	if t.game == nil || t.game.deck == nil {
		return fmt.Errorf("game or deck not initialized")
	}

	// Deal 2 cards to each active player
	for i := 0; i < 2; i++ {
		for _, p := range activePlayers {
			card, ok := t.game.deck.Draw()
			if !ok {
				return fmt.Errorf("failed to deal card to player %s: deck is empty", p.ID)
			}
			p.Hand = append(p.Hand, card)
		}
	}
	return nil
}

// getCurrentPlayerFromUnifiedState returns the current active player from unified state
func (t *Table) getCurrentPlayerFromUnifiedState() *Player {
	if t.game == nil {
		return nil
	}

	// Get active players in order
	activePlayers := t.getActivePlayersInOrder()
	if len(activePlayers) == 0 || t.game.currentPlayer < 0 || t.game.currentPlayer >= len(activePlayers) {
		return nil
	}

	return activePlayers[t.game.currentPlayer]
}

// getActivePlayersInOrder returns active players sorted by table seat
func (t *Table) getActivePlayersInOrder() []*Player {
	activePlayers := make([]*Player, 0, len(t.players))
	for _, p := range t.players {
		if p.IsActiveInGame() {
			activePlayers = append(activePlayers, p)
		}
	}

	// Sort by TableSeat for consistent ordering
	sort.Slice(activePlayers, func(i, j int) bool {
		return activePlayers[i].TableSeat < activePlayers[j].TableSeat
	})

	return activePlayers
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
		for _, p := range t.players {
			if p.IsAtTable() && p.Balance >= t.config.BigBlind {
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

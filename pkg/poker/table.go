package poker

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
)

// TableConfig holds configuration for a new poker table
type TableConfig struct {
	ID            string
	HostID        string
	BuyIn         int64 // DCR amount required to join table (in atoms)
	MinPlayers    int
	MaxPlayers    int
	SmallBlind    int64 // Poker chips amount for small blind
	BigBlind      int64 // Poker chips amount for big blind
	MinBalance    int64 // Minimum DCR account balance required (in atoms)
	StartingChips int64 // Poker chips each player starts with in the game
	TimeBank      time.Duration
}

// BlindPostedCallback is called when a blind is posted
type BlindPostedCallback func(tableID, playerID string, amount int64, isSmallBlind bool)

// Table represents a poker table
type Table struct {
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
	// Callback for when blinds are posted
	blindPostedCallback BlindPostedCallback
}

// NewTable creates a new poker table
func NewTable(cfg TableConfig) *Table {
	return &Table{
		config:         cfg,
		players:        make(map[string]*Player),
		createdAt:      time.Now(),
		lastAction:     time.Now(),
		actionsInRound: 0,
	}
}

// SetBlindPostedCallback sets the callback for when blinds are posted
func (t *Table) SetBlindPostedCallback(callback BlindPostedCallback) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blindPostedCallback = callback
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

	t.allPlayersReady = true
	return true
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

	// Create new game and populate it with references to our table players
	t.game = NewGame(GameConfig{
		NumPlayers:    len(activePlayers),
		StartingChips: t.config.StartingChips,
	})

	// Populate game.players with references to the same Player objects from the table
	// This creates a unified player state - no duplication, just shared references
	copy(t.game.players, activePlayers)

	// Deal initial cards to all active players (2 cards each)
	err := t.dealCardsToPlayers(activePlayers)
	if err != nil {
		return fmt.Errorf("failed to deal cards: %v", err)
	}

	// Post blinds before setting phase to PRE_FLOP
	err = t.postBlinds()
	if err != nil {
		return fmt.Errorf("failed to post blinds: %v", err)
	}

	// Initialize phase to PRE_FLOP so betting can begin immediately
	t.game.phase = pokerrpc.GamePhase_PRE_FLOP

	// Initialize the current player (first to act after blinds are posted)
	t.initializeCurrentPlayer()

	gameStartTime := time.Now()

	// Reset all players' LastAction for timeout management
	earlyTime := gameStartTime.Add(-t.config.TimeBank)
	for _, p := range t.players {
		if p.IsAtTable() {
			p.LastAction = earlyTime
		}
	}

	// Set the current player's LastAction to now so their timeout timer starts
	if currentPlayer := t.getCurrentPlayerFromUnifiedState(); currentPlayer != nil {
		currentPlayer.LastAction = gameStartTime
	}

	// Mark that the game has started
	t.gameStarted = true

	// Reset actions counter for new game
	t.actionsInRound = 0

	t.lastAction = gameStartTime
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
			t.game.AddToPot(delta)
		}
		// Increment actions counter for this betting round
		t.actionsInRound++
		// Advance to next player after action
		t.advanceToNextPlayerLocked()
	}

	// Possibly advance phase if betting round is complete
	t.maybeAdvancePhase()

	t.lastAction = time.Now()
	return nil
}

// GetPot returns the current pot size
func (t *Table) GetPot() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.game == nil {
		return 0
	}
	return t.game.GetPot()
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

// Subscribe creates a channel that sends regular game updates. The updates include
// all player information, with cards visible only to the requesting player or during showdown.
func (t *Table) Subscribe(ctx context.Context, requestingPlayerID string) chan *pokerrpc.GameUpdate {
	updates := make(chan *pokerrpc.GameUpdate)
	go func() {
		defer close(updates)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.mu.RLock()

				players := make([]*pokerrpc.Player, 0, len(t.players))
				for _, p := range t.players {
					// Create player update with complete information from the unified player state
					player := &pokerrpc.Player{
						Id:         p.ID,
						Balance:    p.Balance,
						IsReady:    p.IsReady,
						Folded:     p.HasFolded,
						CurrentBet: p.HasBet,
					}

					// Only include hand cards if this is the requesting player's own data
					// or if the game is in showdown phase
					if (p.ID == requestingPlayerID) || (t.game != nil && t.game.GetPhase() == pokerrpc.GamePhase_SHOWDOWN) {
						player.Hand = make([]*pokerrpc.Card, len(p.Hand))
						for i, card := range p.Hand {
							player.Hand[i] = &pokerrpc.Card{
								Suit:  card.GetSuit(),
								Value: card.GetValue(),
							}
						}
					}

					players = append(players, player)
				}

				// Sort by player ID to ensure consistent ordering
				// This prevents players from appearing to move up/down in the UI
				sort.Slice(players, func(i, j int) bool {
					return players[i].Id < players[j].Id
				})

				var currentPlayerID string
				if t.gameStarted && t.game != nil {
					currentPlayerID = t.GetCurrentPlayerID()
				}

				var communityCards []*pokerrpc.Card
				if t.game != nil {
					for _, c := range t.game.GetCommunityCards() {
						communityCards = append(communityCards, &pokerrpc.Card{
							Suit:  c.GetSuit(),
							Value: c.GetValue(),
						})
					}
				}

				update := &pokerrpc.GameUpdate{
					TableId:         t.config.ID,
					Phase:           t.GetGamePhase(),
					Players:         players,
					CommunityCards:  communityCards,
					Pot:             t.GetPot(),
					CurrentBet:      t.GetCurrentBet(),
					CurrentPlayer:   currentPlayerID,
					GameStarted:     t.gameStarted,
					PlayersRequired: int32(t.config.MinPlayers),
					PlayersJoined:   int32(len(t.players)),
				}

				t.mu.RUnlock()

				select {
				case updates <- update:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return updates
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
			t.advanceToNextPlayerLocked()
		} else {
			// Auto-fold the current player
			currentPlayer.HasFolded = true

			// Advance to next player
			t.advanceToNextPlayerLocked()
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

	// Count active players (non-folded)
	activePlayers := 0
	for _, p := range t.players {
		if !p.HasFolded {
			activePlayers++
		}
	}

	// If zero or one active player, move to showdown
	if activePlayers <= 1 {
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

	if t.game == nil || len(t.game.players) == 0 {
		return ""
	}

	if t.game.currentPlayer < 0 || t.game.currentPlayer >= len(t.game.players) {
		return ""
	}

	return t.game.players[t.game.currentPlayer].ID
}

// AdvanceToNextPlayer moves to the next active player
func (t *Table) AdvanceToNextPlayer() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.advanceToNextPlayerLocked()
}

// advanceToNextPlayerLocked is the internal implementation that assumes the lock is already held
func (t *Table) advanceToNextPlayerLocked() {
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

// initializeCurrentPlayerLocked is the internal implementation that assumes the lock is already held
func (t *Table) initializeCurrentPlayer() {
	if t.game == nil || len(t.game.players) == 0 {
		return
	}

	numPlayers := len(t.game.players)

	// In pre-flop, start with Under the Gun (player after big blind)
	if t.game.phase == pokerrpc.GamePhase_PRE_FLOP {
		if numPlayers == 2 {
			// In heads-up, after blinds are posted, small blind acts first
			t.game.currentPlayer = (t.game.dealer + 1) % numPlayers
		} else {
			// In multi-way, Under the Gun acts first (after big blind)
			t.game.currentPlayer = (t.game.dealer + 3) % numPlayers
		}
	} else {
		// In post-flop streets, start with player after dealer (small blind position)
		t.game.currentPlayer = (t.game.dealer + 1) % numPlayers
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

	// Update the shared player object (this updates both table and game state automatically)
	player.SetGameState("FOLDED")
	player.LastAction = time.Now()

	// Update game state
	if t.gameStarted && t.game != nil {
		// Increment actions counter for this betting round
		t.actionsInRound++
		// Advance to next player after fold action
		t.advanceToNextPlayerLocked()
	}

	// Possibly advance phase if betting round is complete
	t.maybeAdvancePhase()

	t.lastAction = time.Now()
	return nil
}

// postBlinds posts blinds before setting phase to PRE_FLOP
func (t *Table) postBlinds() error {
	if t.game == nil {
		return fmt.Errorf("game not started")
	}

	numPlayers := len(t.game.players)
	if numPlayers < 2 {
		return fmt.Errorf("not enough players for blinds")
	}

	// Small blind position
	var smallBlindIdx int
	if numPlayers == 2 {
		// In heads-up, dealer posts small blind
		smallBlindIdx = t.game.dealer
	} else {
		// In multi-way, player after dealer posts small blind
		smallBlindIdx = (t.game.dealer + 1) % numPlayers
	}

	smallBlindGamePlayer := t.game.players[smallBlindIdx]
	smallBlind := t.config.SmallBlind
	if smallBlind > smallBlindGamePlayer.Balance {
		return fmt.Errorf("insufficient balance for small blind")
	}

	// Update the unified player object
	smallBlindGamePlayer.Balance -= smallBlind
	smallBlindGamePlayer.HasBet = smallBlind
	t.game.AddToPotForPlayer(smallBlindIdx, smallBlind)

	// Call the callback for small blind if set
	if t.blindPostedCallback != nil {
		t.blindPostedCallback(t.config.ID, smallBlindGamePlayer.ID, smallBlind, true)
	}

	// Big blind position
	var bigBlindIdx int
	if numPlayers == 2 {
		// In heads-up, other player posts big blind
		bigBlindIdx = (t.game.dealer + 1) % numPlayers
	} else {
		// In multi-way, two positions after dealer posts big blind
		bigBlindIdx = (t.game.dealer + 2) % numPlayers
	}
	bigBlindGamePlayer := t.game.players[bigBlindIdx]
	bigBlind := t.config.BigBlind
	if bigBlind > bigBlindGamePlayer.Balance {
		return fmt.Errorf("insufficient balance for big blind")
	}

	// Update the unified player object
	bigBlindGamePlayer.Balance -= bigBlind
	bigBlindGamePlayer.HasBet = bigBlind
	t.game.AddToPotForPlayer(bigBlindIdx, bigBlind)

	// Set the current bet to the big blind amount
	t.game.currentBet = bigBlind

	// Call the callback for big blind if set
	if t.blindPostedCallback != nil {
		t.blindPostedCallback(t.config.ID, bigBlindGamePlayer.ID, bigBlind, false)
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

	// If only one player remains, they win automatically without hand evaluation
	if len(activePlayers) <= 1 {
		// Award the pot to the remaining player (if any)
		if len(activePlayers) == 1 {
			winnings := t.game.GetPot()
			activePlayers[0].Balance += winnings
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

			// Distribute pots to winners
			t.game.potManager.DistributePots(t.game.players)
		} else {
			// Can't properly evaluate hands - award pot to first active player
			// This is a fallback for incomplete games
			if len(activePlayers) > 0 {
				winnings := t.game.GetPot()
				activePlayers[0].Balance += winnings
			}
		}
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

	// Reset all players' hand-specific state
	for _, p := range t.players {
		p.HasFolded = false
		p.HasBet = 0
		p.Hand = nil
		p.HandValue = nil
		p.HandDescription = ""
	}

	t.actionsInRound = 0
	t.lastAction = time.Now()

	// Auto-start next hand if we have enough players
	if playersReadyForNextHand >= t.config.MinPlayers {
		// Start a new hand automatically
		err := t.startNewHand()
		if err != nil {
			// If we can't start a new hand, stop the game
			t.gameStarted = false
			t.game = nil
		}
	} else {
		// Not enough players - stop the game
		t.gameStarted = false
		t.game = nil
	}
}

// startNewHand starts a new hand without requiring all players to be ready again
func (t *Table) startNewHand() error {
	// Check if we have enough players with sufficient balance
	activePlayers := make([]*Player, 0, len(t.players))
	for _, p := range t.players {
		if p.IsAtTable() && p.Balance >= t.config.BigBlind { // Player must have at least big blind to play
			activePlayers = append(activePlayers, p)
		}
	}

	if len(activePlayers) < t.config.MinPlayers {
		return fmt.Errorf("not enough players with sufficient balance to start new hand")
	}

	// Sort players by TableSeat for consistent ordering
	sort.Slice(activePlayers, func(i, j int) bool {
		return activePlayers[i].TableSeat < activePlayers[j].TableSeat
	})

	// Create new game and populate it with references to our table players
	t.game = NewGame(GameConfig{
		NumPlayers:    len(activePlayers),
		StartingChips: t.config.StartingChips,
	})

	// Populate game.players with references to the same Player objects from the table
	// This creates a unified player state - no duplication, just shared references
	copy(t.game.players, activePlayers)

	// Deal initial cards to all active players (2 cards each)
	err := t.dealCardsToPlayers(activePlayers)
	if err != nil {
		return fmt.Errorf("failed to deal cards: %v", err)
	}

	// Post blinds before setting phase to PRE_FLOP
	err = t.postBlinds()
	if err != nil {
		return fmt.Errorf("failed to post blinds: %v", err)
	}

	// Initialize phase to PRE_FLOP so betting can begin immediately
	t.game.phase = pokerrpc.GamePhase_PRE_FLOP

	// Initialize the current player (first to act after blinds are posted)
	t.initializeCurrentPlayer()

	gameStartTime := time.Now()

	// Reset all players' LastAction for timeout management
	earlyTime := gameStartTime.Add(-t.config.TimeBank)
	for _, p := range t.players {
		if p.IsAtTable() {
			p.LastAction = earlyTime
		}
	}

	// Set the current player's LastAction to now so their timeout timer starts
	if t.game.currentPlayer >= 0 && t.game.currentPlayer < len(t.game.players) {
		if !t.game.players[t.game.currentPlayer].HasFolded {
			t.game.players[t.game.currentPlayer].LastAction = gameStartTime
		}
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

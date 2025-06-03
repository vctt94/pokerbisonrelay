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
	ID         string
	HostID     string
	BuyIn      int64
	MinPlayers int
	MaxPlayers int
	SmallBlind int64
	BigBlind   int64
	MinBalance int64
	TimeBank   time.Duration
}

// Table represents a poker table
type Table struct {
	config          TableConfig
	players         map[string]*Player
	game            *Game
	mu              sync.RWMutex
	createdAt       time.Time
	lastAction      time.Time
	gameStarted     bool
	allPlayersReady bool
	// Track actions in current betting round
	actionsInRound int
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

// AddPlayer adds a player to the table
func (t *Table) AddPlayer(playerID string, balance int64) error {
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

	// Check if player has enough balance
	if balance < t.config.BuyIn {
		return fmt.Errorf("insufficient balance for buy-in")
	}

	// Add player to table
	t.players[playerID] = &Player{
		ID:         playerID,
		Balance:    balance,
		TableSeat:  len(t.players),
		IsReady:    false,
		LastAction: time.Now(),
	}

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

	// Create new game with proper player mapping
	players := make([]Player, 0, len(t.players))
	i := 0
	for _, p := range t.players {
		// Create game player from table player
		gamePlayer := Player{
			ID:        p.ID,
			Name:      p.Name,
			Balance:   p.Balance,
			TableSeat: i,
			HasFolded: false,
			HasBet:    0,
			IsReady:   p.IsReady,
		}
		players = append(players, gamePlayer)
		i++
	}

	t.game = NewGame(GameConfig{
		NumPlayers: len(players),
	})

	// Set up game players
	t.game.players = make([]*Player, len(players))
	for i := range players {
		t.game.players[i] = &players[i]
	}

	// Deal initial cards to all players (2 cards each)
	err := t.game.DealCards()
	if err != nil {
		return fmt.Errorf("failed to deal cards: %v", err)
	}

	// Initialize phase to PRE_FLOP so betting can begin immediately.
	t.game.phase = pokerrpc.GamePhase_PRE_FLOP

	// Initialize the current player (first to act after dealer)
	t.initializeCurrentPlayer()

	gameStartTime := time.Now()

	// Reset all players' LastAction to a time in the past so they don't timeout
	// before it's their turn, except for the current player
	earlyTime := gameStartTime.Add(-t.config.TimeBank)
	for _, p := range t.players {
		p.LastAction = earlyTime
	}

	// Set the current player's LastAction to now so their timeout timer starts
	if t.game.currentPlayer >= 0 && t.game.currentPlayer < len(t.game.players) {
		currentPlayerID := t.game.players[t.game.currentPlayer].ID
		if currentPlayer, exists := t.players[currentPlayerID]; exists {
			currentPlayer.LastAction = gameStartTime
		}
	}

	// Mark that the game has started so that external callers can query this
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
	status += fmt.Sprintf("Blinds: %.8f/%.8f DCR\n",
		float64(t.config.SmallBlind)/1e8, float64(t.config.BigBlind)/1e8)

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
	// This prevents players from appearing to move up/down in the UI
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

// MakeBet handles a player making a bet
func (t *Table) MakeBet(playerID string, amount int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	player, exists := t.players[playerID]
	if !exists {
		return fmt.Errorf("player not at table")
	}

	// Amount represents the desired TOTAL amount the player wishes to have
	// committed in this betting round. Compute the delta relative to what
	// they have already put in (player.HasBet).
	if amount < player.HasBet {
		return fmt.Errorf("cannot decrease bet")
	}

	delta := amount - player.HasBet

	// Make sure player has enough balance for the delta (only if there's a delta)
	if delta > 0 && delta > player.Balance {
		return fmt.Errorf("insufficient balance")
	}

	// Update player state
	if delta > 0 {
		player.Balance -= delta
		player.HasBet = amount
	}
	player.LastAction = time.Now()

	// Update game state when running
	if t.gameStarted && t.game != nil {
		// Keep track of largest bet so far in the round
		if amount > t.game.currentBet {
			t.game.currentBet = amount
		}

		// Add to pot only if there's actually money being put in
		if delta > 0 {
			t.game.AddToPot(delta)
		}

		// Increment actions counter for this betting round
		t.actionsInRound++

		// Advance to next player after action (including checks)
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
					// Create player update with complete information
					player := &pokerrpc.Player{
						Id:         p.ID,
						Balance:    p.Balance,
						IsReady:    p.IsReady,
						Folded:     p.HasFolded,
						CurrentBet: p.HasBet,
					}

					// Find the corresponding game player to get the hand cards
					var gamePlayer *Player
					if t.game != nil && len(t.game.players) > 0 {
						for _, gp := range t.game.players {
							if gp.ID == p.ID {
								gamePlayer = gp
								break
							}
						}
					}

					// Only include hand cards if this is the requesting player's own data
					// or if the game is in showdown phase
					if (p.ID == requestingPlayerID) || (t.game != nil && t.game.GetPhase() == pokerrpc.GamePhase_SHOWDOWN) {
						if gamePlayer != nil {
							player.Hand = make([]*pokerrpc.Card, len(gamePlayer.Hand))
							for i, card := range gamePlayer.Hand {
								player.Hand[i] = &pokerrpc.Card{
									Suit:  card.GetSuit(),
									Value: card.GetValue(),
								}
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

// HandleTimeouts iterates over players and auto-folds those whose timebank expired.
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

	// Find the current player in table players
	currentPlayer, exists := t.players[currentPlayerID]
	if !exists || currentPlayer.HasFolded {
		return
	}

	// Check if current player has timed out
	if now.Sub(currentPlayer.LastAction) > t.config.TimeBank {
		// Auto-fold the current player
		currentPlayer.HasFolded = true

		// Advance to next player
		t.advanceToNextPlayerLocked()

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
		currentPlayerID := t.game.players[t.game.currentPlayer].ID
		if currentPlayer, exists := t.players[currentPlayerID]; exists && !currentPlayer.HasFolded {
			currentPlayer.LastAction = time.Now()
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

		// Map game player to table player to check if folded
		gamePlayer := t.game.players[t.game.currentPlayer]
		if tablePlayer, exists := t.players[gamePlayer.ID]; exists && !tablePlayer.HasFolded {
			// Set the new current player's LastAction to now so their timeout timer starts
			tablePlayer.LastAction = time.Now()
			break
		}
	}
}

// initializeCurrentPlayerLocked is the internal implementation that assumes the lock is already held
func (t *Table) initializeCurrentPlayer() {
	if t.game == nil || len(t.game.players) == 0 {
		return
	}

	// Start with player after dealer (small blind position)
	t.game.currentPlayer = (t.game.dealer + 1) % len(t.game.players)

	// Ensure we start with an active player
	startingPlayer := t.game.currentPlayer
	for {
		// Map game player to table player to check if folded
		gamePlayer := t.game.players[t.game.currentPlayer]
		if tablePlayer, exists := t.players[gamePlayer.ID]; exists && !tablePlayer.HasFolded {
			break
		}

		t.game.currentPlayer = (t.game.currentPlayer + 1) % len(t.game.players)

		// Prevent infinite loop
		if t.game.currentPlayer == startingPlayer {
			break
		}
	}
}

// HandleFold handles a player folding their hand
func (t *Table) HandleFold(playerID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	player, exists := t.players[playerID]
	if !exists {
		return fmt.Errorf("player not at table")
	}

	// Mark player as folded
	player.HasFolded = true
	player.LastAction = time.Now()

	// Update game state when running
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

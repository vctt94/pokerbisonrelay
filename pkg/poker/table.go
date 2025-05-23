package poker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
)

// TableConfig holds configuration for a new poker table
type TableConfig struct {
	ID         string
	CreatorID  string
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
}

// NewTable creates a new poker table
func NewTable(cfg TableConfig) *Table {
	return &Table{
		config:     cfg,
		players:    make(map[string]*Player),
		createdAt:  time.Now(),
		lastAction: time.Now(),
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

	// Create new game
	players := make([]Player, 0, len(t.players))
	for _, p := range t.players {
		players = append(players, *p)
	}

	t.game = NewGame(GameConfig{
		NumPlayers: len(players),
		MaxRounds:  100, // TODO: Make configurable
	})

	// Initialize phase to PRE_FLOP so betting can begin immediately.
	t.game.phase = pokerrpc.GamePhase_PRE_FLOP

	// Mark that the game has started so that external callers can query this
	t.gameStarted = true

	t.lastAction = time.Now()
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
	if delta == 0 {
		// This is effectively a check/call with no additional chips
		return nil
	}

	// Make sure player has enough balance for the delta
	if delta > player.Balance {
		return fmt.Errorf("insufficient balance")
	}

	// Update player state
	player.Balance -= delta
	player.HasBet = amount
	player.LastAction = time.Now()

	// Update game state when running
	if t.gameStarted && t.game != nil {
		// Keep track of largest bet so far in the round
		if amount > t.game.currentBet {
			t.game.currentBet = amount
		}
		t.game.AddToPot(delta)
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

// Subscribe returns a channel for receiving game updates
func (t *Table) Subscribe(ctx context.Context) chan *pokerrpc.GameUpdate {
	t.mu.Lock()

	updates := make(chan *pokerrpc.GameUpdate, 10)
	go func() {
		defer close(updates)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.mu.RLock()

				players := make([]*pokerrpc.Player, 0, len(t.players))
				for _, p := range t.players {
					players = append(players, &pokerrpc.Player{
						Id:      p.ID,
						Balance: p.Balance,
						IsReady: p.IsReady,
					})
				}

				var currentPlayerID string
				if t.game != nil && len(t.game.players) > 0 {
					if t.game.currentPlayer >= 0 && t.game.currentPlayer < len(t.game.players) {
						currentPlayerID = t.game.players[t.game.currentPlayer].ID
					}
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
	if !t.gameStarted || t.config.TimeBank == 0 {
		return
	}

	now := time.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, p := range t.players {
		if p.HasFolded {
			continue
		}
		if now.Sub(p.LastAction) > t.config.TimeBank {
			p.HasFolded = true
		}
	}
}

// maybeAdvancePhase checks if betting round is finished and progresses the game phase.
func (t *Table) maybeAdvancePhase() {
	if !t.gameStarted || t.game == nil {
		return
	}

	// Determine if all active players have matched the current bet.
	activePlayers := 0
	for _, p := range t.players {
		if p.HasFolded {
			continue
		}
		activePlayers++
		if p.HasBet != t.game.currentBet {
			return // Still players to act
		}
	}

	// If zero or one active player, move to showdown
	if activePlayers <= 1 {
		t.game.phase = pokerrpc.GamePhase_SHOWDOWN
		return
	}

	// Progress to next phase depending on current phase
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

	// Reset player bets for the new round
	for _, p := range t.players {
		p.HasBet = 0
	}

	// Reset game current bet
	t.game.currentBet = 0
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

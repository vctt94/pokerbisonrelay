package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// TableState represents the persistent state of a poker table
type TableState struct {
	ID            string
	HostID        string
	BuyIn         int64
	MinPlayers    int
	MaxPlayers    int
	SmallBlind    int64
	BigBlind      int64
	MinBalance    int64
	StartingChips int64
	GameStarted   bool
	GamePhase     string
	CreatedAt     string
	LastAction    string

	// Game-specific state
	Dealer        int
	CurrentPlayer int
	CurrentBet    int64
	Pot           int64
	Round         int
	BetRound      int

	// Community cards (stored as JSON)
	CommunityCards interface{}

	// Deck state (stored as JSON)
	DeckState interface{}
}

// PlayerState represents the persistent state of a player at a table
type PlayerState struct {
	PlayerID   string
	TableID    string
	TableSeat  int
	IsReady    bool
	LastAction string

	// Game state
	Balance         int64
	StartingBalance int64
	HasBet          int64
	HasFolded       bool
	IsAllIn         bool
	IsDealer        bool
	IsTurn          bool
	GameState       string

	// Hand cards (stored as JSON)
	Hand            interface{}
	HandDescription string
}

// DB represents the database connection
type DB struct {
	*sql.DB
}

// NewDB creates a new database connection
func NewDB(dbPath string) (*DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// Create tables if they don't exist
	if err := createTables(db); err != nil {
		return nil, err
	}

	return &DB{db}, nil
}

// createTables creates the necessary database tables
func createTables(db *sql.DB) error {
	// Create players table
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS players (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			balance INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Create transactions table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS transactions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			player_id TEXT NOT NULL,
			amount INTEGER NOT NULL,
			type TEXT NOT NULL,
			description TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (player_id) REFERENCES players(id)
		)
	`)
	if err != nil {
		return err
	}

	// Create table_states table for persisting game state
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS table_states (
			id TEXT PRIMARY KEY,
			host_id TEXT NOT NULL,
			buy_in INTEGER NOT NULL,
			min_players INTEGER NOT NULL,
			max_players INTEGER NOT NULL,
			small_blind INTEGER NOT NULL,
			big_blind INTEGER NOT NULL,
			min_balance INTEGER NOT NULL,
			starting_chips INTEGER NOT NULL,
			game_started BOOLEAN NOT NULL DEFAULT FALSE,
			game_phase TEXT NOT NULL DEFAULT 'WAITING',
			dealer INTEGER DEFAULT -1,
			current_player INTEGER DEFAULT -1,
			current_bet INTEGER DEFAULT 0,
			pot INTEGER DEFAULT 0,
			round_num INTEGER DEFAULT 0,
			bet_round INTEGER DEFAULT 0,
			community_cards TEXT DEFAULT '[]',
			deck_state TEXT DEFAULT '[]',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			last_action TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Create player_states table for persisting player state at tables
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS player_states (
			player_id TEXT NOT NULL,
			table_id TEXT NOT NULL,
			table_seat INTEGER NOT NULL,
			is_ready BOOLEAN NOT NULL DEFAULT FALSE,
			balance INTEGER NOT NULL DEFAULT 0,
			starting_balance INTEGER NOT NULL DEFAULT 0,
			has_bet INTEGER NOT NULL DEFAULT 0,
			has_folded BOOLEAN NOT NULL DEFAULT FALSE,
			is_all_in BOOLEAN NOT NULL DEFAULT FALSE,
			is_dealer BOOLEAN NOT NULL DEFAULT FALSE,
			is_turn BOOLEAN NOT NULL DEFAULT FALSE,
			game_state TEXT NOT NULL DEFAULT 'AT_TABLE',
			hand TEXT DEFAULT '[]',
			hand_description TEXT DEFAULT '',
			last_action TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (player_id, table_id),
			FOREIGN KEY (table_id) REFERENCES table_states(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return err
	}

	return nil
}

// GetPlayerBalance returns the current balance of a player
func (db *DB) GetPlayerBalance(playerID string) (int64, error) {
	var balance int64
	err := db.QueryRow("SELECT balance FROM players WHERE id = ?", playerID).Scan(&balance)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("player not found")
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get player balance: %v", err)
	}
	return balance, nil
}

// UpdatePlayerBalance updates a player's balance and records the transaction
func (db *DB) UpdatePlayerBalance(playerID string, amount int64, transactionType, description string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Update player balance
	_, err = tx.Exec(`
		INSERT INTO players (id, name, balance)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET balance = balance + ?
	`, playerID, playerID, amount, amount)
	if err != nil {
		return err
	}

	// Record transaction
	_, err = tx.Exec(`
		INSERT INTO transactions (player_id, amount, type, description)
		VALUES (?, ?, ?, ?)
	`, playerID, amount, transactionType, description)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.DB.Close()
}

// SaveTableState saves the table state to the database
func (db *DB) SaveTableState(tableState *TableState) error {
	// Convert community cards and deck state to JSON
	communityCardsJSON, _ := json.Marshal(tableState.CommunityCards)
	deckStateJSON, _ := json.Marshal(tableState.DeckState)

	_, err := db.Exec(`
		INSERT OR REPLACE INTO table_states (
			id, host_id, buy_in, min_players, max_players, small_blind, big_blind,
			min_balance, starting_chips, game_started, game_phase, dealer,
			current_player, current_bet, pot, round_num, bet_round,
			community_cards, deck_state, last_action
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		tableState.ID, tableState.HostID, tableState.BuyIn, tableState.MinPlayers, tableState.MaxPlayers,
		tableState.SmallBlind, tableState.BigBlind, tableState.MinBalance, tableState.StartingChips,
		tableState.GameStarted, tableState.GamePhase, tableState.Dealer, tableState.CurrentPlayer,
		tableState.CurrentBet, tableState.Pot, tableState.Round, tableState.BetRound,
		string(communityCardsJSON), string(deckStateJSON), time.Now(),
	)
	return err
}

// LoadTableState loads the table state from the database
func (db *DB) LoadTableState(tableID string) (*TableState, error) {
	var ts TableState
	var communityCardsJSON, deckStateJSON string

	err := db.QueryRow(`
		SELECT id, host_id, buy_in, min_players, max_players, small_blind, big_blind,
		       min_balance, starting_chips, game_started, game_phase, dealer,
		       current_player, current_bet, pot, round_num, bet_round,
		       community_cards, deck_state, created_at, last_action
		FROM table_states WHERE id = ?
	`, tableID).Scan(
		&ts.ID, &ts.HostID, &ts.BuyIn, &ts.MinPlayers, &ts.MaxPlayers,
		&ts.SmallBlind, &ts.BigBlind, &ts.MinBalance, &ts.StartingChips,
		&ts.GameStarted, &ts.GamePhase, &ts.Dealer, &ts.CurrentPlayer,
		&ts.CurrentBet, &ts.Pot, &ts.Round, &ts.BetRound,
		&communityCardsJSON, &deckStateJSON, &ts.CreatedAt, &ts.LastAction,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("table state not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load table state: %v", err)
	}

	// Preserve the raw JSON strings for higher-level callers. They may choose
	// when and how to unmarshal these fields (for example, during server-side
	// restoration where they are expected to be provided as JSON strings).
	ts.CommunityCards = communityCardsJSON
	ts.DeckState = deckStateJSON

	return &ts, nil
}

// DeleteTableState deletes the table state from the database
func (db *DB) DeleteTableState(tableID string) error {
	_, err := db.Exec("DELETE FROM table_states WHERE id = ?", tableID)
	return err
}

// SavePlayerState saves the player state to the database
func (db *DB) SavePlayerState(tableID string, playerState *PlayerState) error {
	// Convert hand to JSON
	handJSON, _ := json.Marshal(playerState.Hand)

	_, err := db.Exec(`
		INSERT OR REPLACE INTO player_states (
			player_id, table_id, table_seat, is_ready,
			balance, starting_balance, has_bet, has_folded, is_all_in,
			is_dealer, is_turn, game_state, hand, hand_description, last_action
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		playerState.PlayerID, tableID, playerState.TableSeat, playerState.IsReady,
		playerState.Balance, playerState.StartingBalance, playerState.HasBet, playerState.HasFolded,
		playerState.IsAllIn, playerState.IsDealer, playerState.IsTurn, playerState.GameState,
		string(handJSON), playerState.HandDescription, time.Now(),
	)
	return err
}

// LoadPlayerStates loads all player states for a table from the database
func (db *DB) LoadPlayerStates(tableID string) ([]*PlayerState, error) {
	rows, err := db.Query(`
		SELECT player_id, table_id, table_seat, is_ready,
		       balance, starting_balance, has_bet, has_folded, is_all_in,
		       is_dealer, is_turn, game_state, hand, hand_description, last_action
		FROM player_states WHERE table_id = ?
	`, tableID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var playerStates []*PlayerState
	for rows.Next() {
		var ps PlayerState
		var handJSON string

		err := rows.Scan(
			&ps.PlayerID, &ps.TableID, &ps.TableSeat, &ps.IsReady,
			&ps.Balance, &ps.StartingBalance, &ps.HasBet, &ps.HasFolded, &ps.IsAllIn,
			&ps.IsDealer, &ps.IsTurn, &ps.GameState, &handJSON, &ps.HandDescription,
			&ps.LastAction,
		)
		if err != nil {
			return nil, err
		}

		// Keep the raw JSON string so that higher layers can decide when/how
		// to unmarshal it (they may want to treat an empty slice differently
		// or defer decoding until they know the concrete type).
		ps.Hand = handJSON

		playerStates = append(playerStates, &ps)
	}

	return playerStates, nil
}

// DeletePlayerState deletes a player's state from a table
func (db *DB) DeletePlayerState(tableID, playerID string) error {
	_, err := db.Exec("DELETE FROM player_states WHERE table_id = ? AND player_id = ?", tableID, playerID)
	return err
}

// GetAllTableIDs returns all table IDs from the database
func (db *DB) GetAllTableIDs() ([]string, error) {
	rows, err := db.Query("SELECT id FROM table_states")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tableIDs []string
	for rows.Next() {
		var tableID string
		err := rows.Scan(&tableID)
		if err != nil {
			return nil, err
		}
		tableIDs = append(tableIDs, tableID)
	}

	return tableIDs, nil
}

// SaveSnapshot saves the provided table state together with all associated player states atomically.
// This method ensures that the table\'s snapshot (and the set of players that belong to it at the
// time of the snapshot) are always consistent in the database. Existing player_states for the table
// are removed before the new set is inserted so that stale player rows are not resurrected when a
// table is later re-loaded from storage.
func (db *DB) SaveSnapshot(tableState *TableState, playerStates []*PlayerState) error {
	// Convert complex fields to JSON up front so that we can reuse them in the transaction.
	communityCardsJSON, _ := json.Marshal(tableState.CommunityCards)
	deckStateJSON, _ := json.Marshal(tableState.DeckState)

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	// In case of any failure ensure we rollback.
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Upsert (insert or replace) the table state.
	_, err = tx.Exec(`
		INSERT OR REPLACE INTO table_states (
			id, host_id, buy_in, min_players, max_players, small_blind, big_blind,
			min_balance, starting_chips, game_started, game_phase, dealer,
			current_player, current_bet, pot, round_num, bet_round,
			community_cards, deck_state, last_action
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		tableState.ID, tableState.HostID, tableState.BuyIn, tableState.MinPlayers, tableState.MaxPlayers,
		tableState.SmallBlind, tableState.BigBlind, tableState.MinBalance, tableState.StartingChips,
		tableState.GameStarted, tableState.GamePhase, tableState.Dealer, tableState.CurrentPlayer,
		tableState.CurrentBet, tableState.Pot, tableState.Round, tableState.BetRound,
		string(communityCardsJSON), string(deckStateJSON), time.Now(),
	)
	if err != nil {
		return err
	}

	// Prepare an UPSERT that leaves the is_disconnected column untouched on updates so
	// session-level connection tracking is not overwritten by snapshot saves.
	stmt, err := tx.Prepare(`
		INSERT INTO player_states (
			player_id, table_id, table_seat, is_ready,
			balance, starting_balance, has_bet, has_folded, is_all_in,
			is_dealer, is_turn, game_state, hand, hand_description, last_action
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(player_id, table_id) DO UPDATE SET
			table_seat      = excluded.table_seat,
			is_ready        = excluded.is_ready,
			balance         = excluded.balance,
			starting_balance= excluded.starting_balance,
			has_bet         = excluded.has_bet,
			has_folded      = excluded.has_folded,
			is_all_in       = excluded.is_all_in,
			is_dealer       = excluded.is_dealer,
			is_turn         = excluded.is_turn,
			game_state      = excluded.game_state,
			hand            = excluded.hand,
			hand_description= excluded.hand_description,
			last_action     = excluded.last_action
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, ps := range playerStates {
		handJSON, _ := json.Marshal(ps.Hand)
		_, err = stmt.Exec(
			ps.PlayerID, tableState.ID, ps.TableSeat, ps.IsReady,
			ps.Balance, ps.StartingBalance, ps.HasBet, ps.HasFolded, ps.IsAllIn,
			ps.IsDealer, ps.IsTurn, ps.GameState, string(handJSON), ps.HandDescription, time.Now(),
		)
		if err != nil {
			return err
		}
	}

	// Commit the full snapshot.
	return tx.Commit()
}

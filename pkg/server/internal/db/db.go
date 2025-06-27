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
	PlayerID       string
	TableID        string
	TableSeat      int
	IsReady        bool
	IsDisconnected bool
	LastAction     string

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
			is_disconnected BOOLEAN NOT NULL DEFAULT FALSE,
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

	// Parse JSON fields
	json.Unmarshal([]byte(communityCardsJSON), &ts.CommunityCards)
	json.Unmarshal([]byte(deckStateJSON), &ts.DeckState)

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
			player_id, table_id, table_seat, is_ready, is_disconnected,
			balance, starting_balance, has_bet, has_folded, is_all_in,
			is_dealer, is_turn, game_state, hand, hand_description, last_action
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		playerState.PlayerID, tableID, playerState.TableSeat, playerState.IsReady, playerState.IsDisconnected,
		playerState.Balance, playerState.StartingBalance, playerState.HasBet, playerState.HasFolded,
		playerState.IsAllIn, playerState.IsDealer, playerState.IsTurn, playerState.GameState,
		string(handJSON), playerState.HandDescription, time.Now(),
	)
	return err
}

// LoadPlayerStates loads all player states for a table from the database
func (db *DB) LoadPlayerStates(tableID string) ([]*PlayerState, error) {
	rows, err := db.Query(`
		SELECT player_id, table_id, table_seat, is_ready, is_disconnected,
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
			&ps.IsDisconnected, &ps.Balance, &ps.StartingBalance,
			&ps.HasBet, &ps.HasFolded, &ps.IsAllIn, &ps.IsDealer,
			&ps.IsTurn, &ps.GameState, &handJSON, &ps.HandDescription,
			&ps.LastAction,
		)
		if err != nil {
			return nil, err
		}

		// Parse hand JSON
		json.Unmarshal([]byte(handJSON), &ps.Hand)

		playerStates = append(playerStates, &ps)
	}

	return playerStates, nil
}

// DeletePlayerState deletes a player's state from a table
func (db *DB) DeletePlayerState(tableID, playerID string) error {
	_, err := db.Exec("DELETE FROM player_states WHERE table_id = ? AND player_id = ?", tableID, playerID)
	return err
}

// SetPlayerDisconnected marks a player as disconnected
func (db *DB) SetPlayerDisconnected(tableID, playerID string) error {
	_, err := db.Exec(`
		UPDATE player_states 
		SET is_disconnected = TRUE, last_action = ? 
		WHERE table_id = ? AND player_id = ?
	`, time.Now(), tableID, playerID)
	return err
}

// SetPlayerConnected marks a player as connected
func (db *DB) SetPlayerConnected(tableID, playerID string) error {
	_, err := db.Exec(`
		UPDATE player_states 
		SET is_disconnected = FALSE, last_action = ? 
		WHERE table_id = ? AND player_id = ?
	`, time.Now(), tableID, playerID)
	return err
}

// IsPlayerDisconnected checks if a player is disconnected
func (db *DB) IsPlayerDisconnected(tableID, playerID string) (bool, error) {
	var isDisconnected bool
	err := db.QueryRow(`
		SELECT is_disconnected 
		FROM player_states 
		WHERE table_id = ? AND player_id = ?
	`, tableID, playerID).Scan(&isDisconnected)
	if err == sql.ErrNoRows {
		return false, fmt.Errorf("player state not found")
	}
	if err != nil {
		return false, err
	}
	return isDisconnected, nil
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

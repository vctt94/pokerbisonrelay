package db

import (
	"database/sql"
	"fmt"
)

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

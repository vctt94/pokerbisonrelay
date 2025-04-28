package server

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vctt94/poker-bisonrelay/server/internal/db"
	"github.com/vctt94/poker-bisonrelay/server/types"
)

// Database defines the interface for database operations
type Database interface {
	// GetPlayerBalance returns the current balance of a player
	GetPlayerBalance(playerID string) (int64, error)
	// UpdatePlayerBalance updates a player's balance and records the transaction
	UpdatePlayerBalance(playerID string, amount int64, transactionType, description string) error
	// GetPlayerTransactions returns the transaction history for a player
	GetPlayerTransactions(playerID string, limit int) ([]types.Transaction, error)
	// Close closes the database connection
	Close() error
}

// Transaction represents a player's transaction
type Transaction struct {
	ID          int64
	PlayerID    string
	Amount      int64
	Type        string
	Description string
	CreatedAt   string
}

// NewDatabase creates a new database connection
func NewDatabase(dbPath string) (Database, error) {
	// Ensure the directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %v", err)
	}

	// Create the database
	return db.NewDB(dbPath)
}

package server

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vctt94/pokerbisonrelay/pkg/server/internal/db"
)

// Database defines the interface for database operations
type Database interface {
	// GetPlayerBalance returns the current balance of a player
	GetPlayerBalance(playerID string) (int64, error)
	// UpdatePlayerBalance updates a player's balance and records the transaction
	UpdatePlayerBalance(playerID string, amount int64, transactionType, description string) error

	// Game state persistence
	SaveTableState(tableState *db.TableState) error
	// SaveSnapshot atomically persists a table state together with its related player states.
	SaveSnapshot(tableState *db.TableState, playerStates []*db.PlayerState) error
	LoadTableState(tableID string) (*db.TableState, error)
	DeleteTableState(tableID string) error

	// Player state at table
	SavePlayerState(tableID string, playerState *db.PlayerState) error
	LoadPlayerStates(tableID string) ([]*db.PlayerState, error)
	DeletePlayerState(tableID, playerID string) error

	// Table discovery
	GetAllTableIDs() ([]string, error)

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

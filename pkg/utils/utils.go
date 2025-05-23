package utils

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
)

// FormatCards is a helper function for displaying cards
func FormatCards(cards []*pokerrpc.Card) string {
	if len(cards) == 0 {
		return "None"
	}

	result := ""
	for i, card := range cards {
		if i > 0 {
			result += " "
		}
		result += card.Value + card.Suit
	}

	return result
}

// EnsureDataDirExists creates the datadir and necessary subdirectories if they don't exist
func EnsureDataDirExists(datadir string) error {
	// Create main datadir
	if err := os.MkdirAll(datadir, 0700); err != nil {
		return fmt.Errorf("failed to create datadir %s: %v", datadir, err)
	}

	// Create logs subdirectory
	logsDir := filepath.Join(datadir, "logs")
	if err := os.MkdirAll(logsDir, 0700); err != nil {
		return fmt.Errorf("failed to create logs directory %s: %v", logsDir, err)
	}

	return nil
}

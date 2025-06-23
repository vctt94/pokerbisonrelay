package utils

import (
	"fmt"
	"os"
	"path/filepath"
)

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

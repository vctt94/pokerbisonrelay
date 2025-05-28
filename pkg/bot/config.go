package bot

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vctt94/bisonbotkit/config"
	"github.com/vctt94/bisonbotkit/utils"
)

// BotConfig represents the processed bot configuration
type BotConfig struct {
	Config        *config.BotConfig
	DataDir       string
	ServerAddress string
	CertFile      string
	KeyFile       string
	MaxLogFiles   string
	LogFile       string
}

// LoadBotConfig loads and processes the bot configuration
func LoadBotConfig(appName, datadir string) (*BotConfig, error) {
	// Set up configuration directory
	if datadir == "" {
		datadir = utils.AppDataDir(appName, false)
	}

	// Ensure the log directory exists
	logDir := filepath.Join(datadir, "logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %v", err)
	}

	// Load bot configuration
	cfg, err := config.LoadBotConfig(datadir, appName+".conf")
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %v", err)
	}

	// Read grpchost and grpcport from config file first
	grpcHost := cfg.ExtraConfig["grpchost"]
	grpcPort := cfg.ExtraConfig["grpcport"]

	// Set grpc server address
	if grpcHost == "" {
		return nil, fmt.Errorf("GRPCHost is required")
	}
	if grpcPort == "" {
		return nil, fmt.Errorf("GRPCPort is required")
	}
	serverAddress := fmt.Sprintf("%s:%s", grpcHost, grpcPort)

	return &BotConfig{
		Config:        cfg,
		DataDir:       datadir,
		ServerAddress: serverAddress,
		CertFile:      filepath.Join(datadir, "server.cert"),
		KeyFile:       filepath.Join(datadir, "server.key"),
		MaxLogFiles:   "5",
		LogFile:       filepath.Join(logDir, "pokerbot.log"),
	}, nil
}

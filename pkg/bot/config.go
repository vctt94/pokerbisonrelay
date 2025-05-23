package bot

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vctt94/bisonbotkit/config"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/bisonbotkit/utils"
)

// BotFlags holds all bot command line flags
type BotFlags struct {
	DataDir            *string
	URL                *string
	GRPCServerCertPath *string
	CertFile           *string
	KeyFile            *string
	RPCUser            *string
	RPCPass            *string
	GRPCHost           *string
	GRPCPort           *string
	DebugLevel         *string
}

// BotConfig represents the processed bot configuration
type BotConfig struct {
	Config        *config.BotConfig
	DataDir       string
	ServerAddress string
	CertFile      string
	KeyFile       string
	LogDir        string
}

// RegisterBotFlags registers all bot command line flags
func RegisterBotFlags() *BotFlags {
	return &BotFlags{
		DataDir:            flag.String("datadir", "", "Directory to load config file from"),
		URL:                flag.String("url", "", "URL of the websocket endpoint"),
		GRPCServerCertPath: flag.String("grpcservercert", "", "Path to server.crt file for TLS"),
		CertFile:           flag.String("cert", "", "Path to TLS certificate file"),
		KeyFile:            flag.String("key", "", "Path to TLS key file"),
		RPCUser:            flag.String("rpcuser", "", "RPC user for basic authentication"),
		RPCPass:            flag.String("rpcpass", "", "RPC password for basic authentication"),
		GRPCHost:           flag.String("grpchost", "", "GRPC server hostname"),
		GRPCPort:           flag.String("grpcport", "", "GRPC server port"),
		DebugLevel:         flag.String("debuglevel", "", "Debug level for logging"),
	}
}

// LoadBotConfig loads and processes the bot configuration
func LoadBotConfig(flags *BotFlags, appName string) (*BotConfig, error) {
	// Set up configuration directory
	datadir := *flags.DataDir
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

	// Apply overrides from flags
	if *flags.URL != "" {
		cfg.RPCURL = *flags.URL
	}
	if *flags.GRPCServerCertPath != "" {
		cfg.ServerCertPath = *flags.GRPCServerCertPath
	}
	if *flags.RPCUser != "" {
		cfg.RPCUser = *flags.RPCUser
	}
	if *flags.RPCPass != "" {
		cfg.RPCPass = *flags.RPCPass
	}
	if *flags.DebugLevel != "" {
		cfg.Debug = *flags.DebugLevel
	}

	// Set grpc server address
	serverAddress := ":50051"
	if *flags.GRPCHost != "" && *flags.GRPCPort != "" {
		serverAddress = fmt.Sprintf("%s:%s", *flags.GRPCHost, *flags.GRPCPort)
	}

	return &BotConfig{
		Config:        cfg,
		DataDir:       datadir,
		ServerAddress: serverAddress,
		CertFile:      *flags.CertFile,
		KeyFile:       *flags.KeyFile,
		LogDir:        logDir,
	}, nil
}

// SetupBotLogging sets up logging for bot applications
func SetupBotLogging(logDir, debugLevel string) (*logging.LogBackend, error) {
	logConfig := logging.LogConfig{
		LogFile:     filepath.Join(logDir, "pokerbot.log"),
		DebugLevel:  debugLevel,
		MaxLogFiles: 5,
	}

	logBackend, err := logging.NewLogBackend(logConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logging: %v", err)
	}

	return logBackend, nil
}

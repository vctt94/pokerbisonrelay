package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/vctt94/pokerbisonrelay/pkg/client"
	"github.com/vctt94/pokerbisonrelay/pkg/ui"
)

var (
	// Define command line flags
	dataDir         = flag.String("datadir", "", "Directory to load config file from")
	rpcURL          = flag.String("url", "", "URL of the websocket endpoint")
	grpcServerCert  = flag.String("grpcservercert", "", "Path to server.crt file for TLS")
	brClientCert    = flag.String("brclientcert", "", "Path to brclient rpc.cert file")
	brClientRPCCert = flag.String("brclientrpc.cert", "", "Path to rpc-client.cert file")
	brClientRPCKey  = flag.String("brclientrpc.key", "", "Path to rpc-client.key file")
	rpcUser         = flag.String("rpcuser", "", "RPC user for basic authentication")
	rpcPass         = flag.String("rpcpass", "", "RPC password for basic authentication")
	grpcHost        = flag.String("grpchost", "", "GRPC server hostname")
	grpcPort        = flag.String("grpcport", "", "GRPC server port")
	logFile         = flag.String("logfile", "", "Path to log file")
	maxLogFiles     = flag.Int("maxlogfiles", 10, "Maximum number of log files")
	maxBufferLines  = flag.Int("maxbufferlines", 1000, "Maximum number of buffer lines")
	debug           = flag.String("debug", "", "Debug level for logging")
)

func main() {
	flag.Parse()

	// Create and load configuration
	cfg := &client.PokerClientConfig{}
	if err := cfg.LoadConfig("pokerclient", *dataDir); err != nil {
		fmt.Printf("Configuration error: %v\n", err)
		os.Exit(1)
	}

	// Override config with command line flags
	flagOverrides := make(map[string]interface{})
	if *rpcURL != "" {
		flagOverrides["rpcurl"] = *rpcURL
	}
	if *grpcServerCert != "" {
		flagOverrides["grpcservercert"] = *grpcServerCert
	}
	if *brClientCert != "" {
		flagOverrides["brclientcert"] = *brClientCert
	}
	if *brClientRPCCert != "" {
		flagOverrides["brclientrpccert"] = *brClientRPCCert
	}
	if *brClientRPCKey != "" {
		flagOverrides["brclientrpckey"] = *brClientRPCKey
	}
	if *rpcUser != "" {
		flagOverrides["rpcuser"] = *rpcUser
	}
	if *rpcPass != "" {
		flagOverrides["rpcpass"] = *rpcPass
	}
	if *grpcHost != "" {
		flagOverrides["grpchost"] = *grpcHost
	}
	if *grpcPort != "" {
		flagOverrides["grpcport"] = *grpcPort
	}
	if *logFile != "" {
		flagOverrides["logfile"] = *logFile
	}
	if *maxLogFiles != 10 { // Only override if not default
		flagOverrides["maxlogfiles"] = *maxLogFiles
	}
	if *maxBufferLines != 1000 { // Only override if not default
		flagOverrides["maxbufferlines"] = *maxBufferLines
	}
	if *debug != "" {
		flagOverrides["debug"] = *debug
	}

	cfg.SetConfigValues(flagOverrides)

	// Validate configuration
	if err := cfg.ValidateConfig(); err != nil {
		fmt.Printf("Configuration validation error: %v\n", err)
		os.Exit(1)
	}

	// Initialize notification manager BEFORE creating the client
	cfg.Notifications = client.NewNotificationManager()

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create poker client with configuration
	pokerClient, err := client.NewPokerClient(ctx, cfg)
	if err != nil {
		fmt.Printf("Failed to create poker client: %v\n", err)
		os.Exit(1)
	}
	defer pokerClient.Close()
	log := pokerClient.BRClient.LogBackend.Logger("pokerclient")

	// Start the notification stream
	if err := pokerClient.StartNotificationStream(ctx); err != nil {
		log.Infof("Failed to start notifications: %v\n", err)
		os.Exit(1)
	}

	// Start the UI with the client's components
	ui.Run(ctx, pokerClient)
}

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/companyzero/bisonrelay/clientrpc/types"
	"github.com/vctt94/bisonbotkit/botclient"
	"github.com/vctt94/bisonbotkit/config"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/bisonbotkit/utils"
	"github.com/vctt94/poker-bisonrelay/rpc/grpc/pokerrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var (
	serverAddr             = flag.String("server", "", "Server address")
	datadir                = flag.String("datadir", "", "Directory to load config file from")
	flagURL                = flag.String("url", "", "URL of the websocket endpoint")
	flagGRPCServerCertPath = flag.String("grpcservercert", "", "Path to server.crt file for TLS")
	flagClientCertPath     = flag.String("clientcert", "", "Path to rpc-client.cert file")
	flagClientKeyPath      = flag.String("clientkey", "", "Path to rpc-client.key file")
	rpcUser                = flag.String("rpcuser", "", "RPC user for basic authentication")
	rpcPass                = flag.String("rpcpass", "", "RPC password for basic authentication")
	grpcHost               = flag.String("grpchost", "localhost", "GRPC server hostname")
	grpcPort               = flag.String("grpcport", "50051", "GRPC server port")
)

func main() {
	flag.Parse()

	// Set up configuration directory
	if *datadir == "" {
		*datadir = utils.AppDataDir("pokerclient", false)
	}

	// Load the configuration
	cfg, err := config.LoadClientConfig(*datadir, "pokerclient.conf")
	if err != nil {
		fmt.Println("Error loading configuration:", err)
		os.Exit(1)
	}

	// Apply overrides from flags
	if *serverAddr != "" {
		cfg.ServerAddr = *serverAddr
	}
	if *flagURL != "" {
		cfg.RPCURL = *flagURL
	}
	if *flagGRPCServerCertPath != "" {
		cfg.ServerCertPath = *flagGRPCServerCertPath
	}
	if *flagClientCertPath != "" {
		cfg.ClientCertPath = *flagClientCertPath
	}
	if *flagClientKeyPath != "" {
		cfg.ClientKeyPath = *flagClientKeyPath
	}
	if *rpcUser != "" {
		cfg.RPCUser = *rpcUser
	}
	if *rpcPass != "" {
		cfg.RPCPass = *rpcPass
	}

	// Construct server address from host and port if provided
	if *grpcHost != "" && *grpcPort != "" {
		cfg.ServerAddr = fmt.Sprintf("%s:%s", *grpcHost, *grpcPort)
	}

	// Set up logging
	useStdout := true
	logBackend, err := logging.NewLogBackend(logging.LogConfig{
		LogFile:        filepath.Join(*datadir, "logs", "pokerclient.log"),
		DebugLevel:     cfg.Debug,
		MaxLogFiles:    10,
		MaxBufferLines: 1000,
		UseStdout:      &useStdout,
	})
	log := logBackend.Logger("PokerClient")

	if err != nil {
		fmt.Errorf("Failed to set up logging: %v", err)
	}

	// Log server address after logger is initialized
	if *grpcHost != "" && *grpcPort != "" {
		log.Infof("Using server address: %s", cfg.ServerAddr)
	}

	// Initialize BisonRelay client
	brClient, err := botclient.NewClient(cfg, logBackend)
	if err != nil {
		log.Errorf("Failed to create bot client: %v", err)
	}

	// Start the RPC client in a goroutine
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go brClient.RunRPC(ctx)

	// Get the client ID
	var publicIdentity types.PublicIdentity
	err = brClient.Chat.UserPublicIdentity(ctx, &types.PublicIdentityReq{}, &publicIdentity)
	if err != nil {
		log.Errorf("Failed to get user public identity: %v", err)
	}

	// Convert the identity to a hex string for use as client ID
	clientID := hex.EncodeToString(publicIdentity.Identity[:])
	log.Errorf("Using client ID: %s", clientID)

	// Connect to the poker server
	var dialOpts []grpc.DialOption

	// Use TLS
	grpcServerCertPath := cfg.GRPCServerCert
	if *flagGRPCServerCertPath != "" {
		grpcServerCertPath = *flagGRPCServerCertPath
	}
	if grpcServerCertPath == "" {
		grpcServerCertPath = filepath.Join(*datadir, "server.cert")
	}
	fmt.Println("Server cert path: ", grpcServerCertPath)

	// Load the server certificate
	pemServerCA, err := os.ReadFile(grpcServerCertPath)
	if err != nil {
		log.Errorf("Failed to read server certificate: %v", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(pemServerCA) {
		log.Error("Failed to add server certificate to pool")
	}

	// Create the TLS credentials with ServerName set to grpcHost
	tlsConfig := &tls.Config{
		RootCAs:    certPool,
		ServerName: *grpcHost,
	}

	creds := credentials.NewTLS(tlsConfig)

	dialOpts = append(dialOpts, grpc.WithTransportCredentials(creds))
	log.Infof("Using TLS connection to the server with hostname: %s", *grpcHost)

	// Create the client connection
	conn, err := grpc.Dial(cfg.ServerAddr, dialOpts...)
	if err != nil {
		log.Errorf("Failed to connect: %v", err)
		return
	}
	defer conn.Close()

	// Create the lobby client
	lobbyClient := pokerrpc.NewLobbyServiceClient(conn)

	// Create the poker client for game actions
	pokerClient := pokerrpc.NewPokerServiceClient(conn)

	// Make sure we have an account
	balanceResp, err := lobbyClient.GetBalance(ctx, &pokerrpc.GetBalanceRequest{
		PlayerId: clientID,
	})
	if err != nil {
		log.Errorf("Could not get balance, initializing account: %v", err)
		// Initialize account with deposit
		updateResp, err := lobbyClient.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    clientID,
			Amount:      1000,
			Description: "Initial deposit",
		})
		if err != nil {
			log.Errorf("Could not initialize balance: %v", err)
			return
		}
		log.Infof("Initialized balance: %d", updateResp.NewBalance)
	} else {
		log.Infof("Current balance: %d", balanceResp.Balance)
	}

	// Start the UI
	RunUI(ctx, clientID, lobbyClient, pokerClient)
}

// formatCards is a helper function for displaying cards
func formatCards(cards []*pokerrpc.Card) string {
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

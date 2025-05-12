package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/companyzero/bisonrelay/clientrpc/types"
	"github.com/vctt94/bisonbotkit/botclient"
	"github.com/vctt94/bisonbotkit/config"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/bisonbotkit/utils"
	"github.com/vctt94/poker-bisonrelay/rpc/grpc/pokerrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// Parse command line flags
	serverAddr := flag.String("server", "localhost:50051", "server address")
	datadir := flag.String("datadir", "", "Directory to load config file from")
	flagURL := flag.String("url", "", "URL of the websocket endpoint")
	flagServerCertPath := flag.String("servercert", "", "Path to rpc.cert file")
	flagClientCertPath := flag.String("clientcert", "", "Path to rpc-client.cert file")
	flagClientKeyPath := flag.String("clientkey", "", "Path to rpc-client.key file")
	rpcUser := flag.String("rpcuser", "", "RPC user for basic authentication")
	rpcPass := flag.String("rpcpass", "", "RPC password for basic authentication")
	flag.Parse()

	// Set up configuration
	if *datadir == "" {
		*datadir = utils.AppDataDir("pokerclient", false)
	}
	cfg, err := config.LoadClientConfig(*datadir, "pokerclient.conf")
	if err != nil {
		fmt.Println("Error loading configuration:", err)
		os.Exit(1)
	}

	// Apply overrides from flags
	if *flagURL != "" {
		cfg.RPCURL = *flagURL
	}
	if *flagServerCertPath != "" {
		cfg.ServerCertPath = *flagServerCertPath
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
	if *serverAddr != "" {
		cfg.ServerAddr = *serverAddr
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
	if err != nil {
		log.Fatalf("Failed to set up logging: %v", err)
	}

	// Initialize BisonRelay client
	brClient, err := botclient.NewClient(cfg, logBackend)
	if err != nil {
		log.Fatalf("Failed to create BisonRelay client: %v", err)
	}

	// Start the RPC client in a goroutine
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go brClient.RunRPC(ctx)

	// Get the client ID
	var publicIdentity types.PublicIdentity
	err = brClient.Chat.UserPublicIdentity(ctx, &types.PublicIdentityReq{}, &publicIdentity)
	if err != nil {
		log.Fatalf("Failed to get user public identity: %v", err)
	}

	// Convert the identity to a hex string for use as client ID
	clientID := hex.EncodeToString(publicIdentity.Identity[:])
	log.Printf("Using client ID: %s", clientID)

	// Connect to the poker server
	conn, err := grpc.Dial(*serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
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
		log.Printf("Could not get balance, initializing account: %v", err)
		// Initialize account with deposit
		updateResp, err := lobbyClient.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    clientID,
			Amount:      1000,
			Description: "Initial deposit",
		})
		if err != nil {
			log.Fatalf("Could not initialize balance: %v", err)
		}
		log.Printf("Initialized balance: %d", updateResp.NewBalance)
	} else {
		log.Printf("Current balance: %d", balanceResp.Balance)
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

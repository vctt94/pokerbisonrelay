package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/companyzero/bisonrelay/clientrpc/types"
	"github.com/vctt94/bisonbotkit/botclient"
	"github.com/vctt94/poker-bisonrelay/pkg/client"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/poker-bisonrelay/pkg/ui"
)

func main() {
	// Register and parse flags
	flags := client.RegisterClientFlags()
	flag.Parse()

	// Load configuration
	cfg, err := client.LoadClientConfig(flags, "pokerclient")
	if err != nil {
		fmt.Printf("Configuration error: %v\n", err)
		os.Exit(1)
	}

	// Set up logging
	logBackend, err := client.SetupClientLogging(cfg.DataDir, cfg.Config.Debug)
	if err != nil {
		fmt.Printf("Logging error: %v\n", err)
		os.Exit(1)
	}

	log := logBackend.Logger("PokerClient")
	log.Infof("Using server address: %s", cfg.Config.ServerAddr)

	// Initialize BisonRelay client
	brClient, err := botclient.NewClient(cfg.Config, logBackend)
	if err != nil {
		log.Errorf("Failed to create bot client: %v", err)
		os.Exit(1)
	}

	// Start the RPC client in a goroutine only if brClient was created successfully
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var clientID string
	if brClient != nil {
		go brClient.RunRPC(ctx)

		// Get the client ID
		var publicIdentity types.PublicIdentity
		err = brClient.Chat.UserPublicIdentity(ctx, &types.PublicIdentityReq{}, &publicIdentity)
		if err != nil {
			log.Errorf("Failed to get user public identity: %v", err)
		}

		// Convert the identity to a hex string for use as client ID
		clientID = hex.EncodeToString(publicIdentity.Identity[:])
	} else {
		// Use a fallback client ID if BR client failed
		clientID = fmt.Sprintf("client-%d", os.Getpid())
		log.Warnf("Using fallback client ID: %s", clientID)
	}

	log.Infof("Using client ID: %s", clientID)

	// Setup GRPC server certificate
	grpcServerCertPath := cfg.Config.GRPCServerCert
	if grpcServerCertPath == "" {
		grpcServerCertPath = filepath.Join(cfg.DataDir, "server.cert")
	}

	log.Infof("Server cert path: %s", grpcServerCertPath)

	// Check if server certificate exists, create default one if not
	if _, err := os.Stat(grpcServerCertPath); os.IsNotExist(err) {
		log.Warnf("Server certificate not found at %s, creating default certificate", grpcServerCertPath)
		if err := client.CreateDefaultServerCert(grpcServerCertPath); err != nil {
			log.Errorf("Failed to create default server certificate: %v", err)
			os.Exit(1)
		}
		log.Infof("Created default server certificate at %s", grpcServerCertPath)
	}

	// Setup GRPC connection
	conn, err := client.SetupGRPCConnection(cfg.Config.ServerAddr, grpcServerCertPath, cfg.GRPCHost)
	if err != nil {
		log.Errorf("Failed to setup GRPC connection: %v", err)
		return
	}
	defer conn.Close()

	log.Infof("Using TLS connection to the server with hostname: %s", cfg.GRPCHost)

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
	ui.Run(ctx, clientID, lobbyClient, pokerClient)
}

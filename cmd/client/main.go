package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/vctt94/poker-bisonrelay/pkg/client"
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

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create the client configuration for the new Client struct
	clientConfig := &client.Config{
		ServerAddr:      cfg.Cfg.ServerAddr,
		DataDir:         cfg.DataDir,
		RPCURL:          cfg.Cfg.RPCURL,
		GRPCServerCert:  cfg.Cfg.GRPCServerCert,
		BRClientCert:    cfg.Cfg.BRClientCert,
		BRClientRPCCert: cfg.Cfg.BRClientRPCCert,
		BRClientRPCKey:  cfg.Cfg.BRClientRPCKey,
		RPCUser:         cfg.Cfg.RPCUser,
		RPCPass:         cfg.Cfg.RPCPass,
		GRPCHost:        cfg.GRPCHost,
		GRPCPort:        *flags.GRPCPort,
	}

	// Create the new client using the refactored NewClient method
	pokerClient, err := client.NewClient(ctx, clientConfig)
	if err != nil {
		fmt.Printf("Failed to create poker client: %v\n", err)
		os.Exit(1)
	}
	defer pokerClient.Close()

	// Start the UI with the client's components
	ui.Run(ctx, pokerClient.ID, pokerClient.LobbyClient, pokerClient.PokerClient)
}

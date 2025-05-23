package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/companyzero/bisonrelay/clientrpc/types"
	"github.com/companyzero/bisonrelay/zkidentity"
	"github.com/decred/dcrd/dcrutil/v4"
	_ "github.com/mattn/go-sqlite3" // SQLite3 driver
	kit "github.com/vctt94/bisonbotkit"
	"github.com/vctt94/poker-bisonrelay/pkg/bot"
	"github.com/vctt94/poker-bisonrelay/pkg/server"
)

func realMain() error {
	// Register and parse flags
	flags := bot.RegisterBotFlags()
	flag.Parse()

	// Load configuration
	cfg, err := bot.LoadBotConfig(flags, "pokerbot")
	if err != nil {
		return fmt.Errorf("configuration error: %v", err)
	}

	// Create channels for handling PMs and tips
	pmChan := make(chan types.ReceivedPM)
	tipChan := make(chan types.ReceivedTip)
	tipProgressChan := make(chan types.TipProgressEvent)

	cfg.Config.PMChan = pmChan
	cfg.Config.TipProgressChan = tipProgressChan
	cfg.Config.TipReceivedChan = tipChan

	botInstance, err := kit.NewBot(cfg.Config)
	if err != nil {
		return fmt.Errorf("failed to create bot: %v", err)
	}
	log := botInstance.LogBackend.Logger("BOT")

	log.Infof("Starting bot...")
	// Set up context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer botInstance.Close()

	// Initialize database
	db, err := server.NewDatabase(filepath.Join(cfg.DataDir, "poker.db"))
	if err != nil {
		return fmt.Errorf("failed to initialize database: %v", err)
	}
	defer db.Close()

	// Initialize and start the gRPC poker server
	grpcServer, grpcLis, err := bot.SetupGRPCServer(cfg.DataDir, cfg.CertFile, cfg.KeyFile, cfg.ServerAddress, db)
	if err != nil {
		return fmt.Errorf("failed to setup gRPC server: %v", err)
	}

	// Initialize bot state
	state := bot.NewState(db)
	go func() {
		log.Infof("Starting gRPC poker server on %s", cfg.ServerAddress)
		if err := grpcServer.Serve(grpcLis); err != nil {
			log.Errorf("gRPC poker server error: %v", err)
		}
	}()
	defer grpcServer.Stop() // Ensure gRPC server is stopped on exit
	// Handle PMs
	go func() {
		for pm := range pmChan {
			state.HandlePM(ctx, botInstance, &pm)
		}
	}()

	// Handle received tips
	go func() {
		for tip := range tipChan {
			var userID zkidentity.ShortID
			userID.FromBytes(tip.Uid)

			log.Infof("Tip received: %.8f DCR from %s",
				dcrutil.Amount(tip.AmountMatoms/1e3).ToCoin(),
				userID.String())

			// Update player balance
			err := db.UpdatePlayerBalance(userID.String(), int64(tip.AmountMatoms/1e3),
				"tip", "Received tip from user")
			if err != nil {
				log.Errorf("Failed to update player balance: %v", err)
				botInstance.SendPM(ctx, userID.String(),
					"Error updating your balance. Please contact support.")
			} else {
				botInstance.SendPM(ctx, userID.String(),
					fmt.Sprintf("Thank you for the tip of %.8f DCR! Your balance has been updated.",
						dcrutil.Amount(tip.AmountMatoms/1e3).ToCoin()))
			}

			botInstance.AckTipReceived(ctx, tip.SequenceId)
		}
	}()

	// Handle tip progress updates
	go func() {
		for progress := range tipProgressChan {
			log.Infof("Tip progress event (sequence ID: %d)", progress.SequenceId)
			err := botInstance.AckTipProgress(ctx, progress.SequenceId)
			if err != nil {
				log.Errorf("Failed to acknowledge tip progress: %v", err)
			}
		}
	}()

	// Run the bot
	err = botInstance.Run(ctx)
	log.Infof("Bot exited: %v", err)
	return err
}

func main() {
	if err := realMain(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

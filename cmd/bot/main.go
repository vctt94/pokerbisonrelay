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

	// Initialize logging
	logBackend, err := bot.SetupBotLogging(cfg.LogDir, cfg.Config.Debug)
	if err != nil {
		return fmt.Errorf("logging error: %v", err)
	}
	defer logBackend.Close()

	log := logBackend.Logger("PokerBot")

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

	go func() {
		log.Infof("Starting gRPC poker server on %s", cfg.ServerAddress)
		if err := grpcServer.Serve(grpcLis); err != nil {
			log.Errorf("gRPC poker server error: %v", err)
		}
	}()
	defer grpcServer.Stop() // Ensure gRPC server is stopped on exit

	// Initialize bot state
	state := bot.NewState(db)

	// Create channels for handling PMs and tips
	pmChan := make(chan types.ReceivedPM)
	tipChan := make(chan types.ReceivedTip)
	tipProgressChan := make(chan types.TipProgressEvent)

	cfg.Config.PMChan = pmChan
	cfg.Config.PMLog = logBackend.Logger("PM")
	cfg.Config.TipLog = logBackend.Logger("TIP")
	cfg.Config.TipProgressChan = tipProgressChan
	cfg.Config.TipReceivedLog = logBackend.Logger("TIP_RECEIVED")
	cfg.Config.TipReceivedChan = tipChan

	log.Infof("Starting bot...")

	botInstance, err := kit.NewBot(cfg.Config, logBackend)
	if err != nil {
		return fmt.Errorf("failed to create bot: %v", err)
	}

	// Set up context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

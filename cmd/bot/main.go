package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/companyzero/bisonrelay/clientrpc/types"
	"github.com/companyzero/bisonrelay/zkidentity"
	"github.com/decred/dcrd/dcrutil/v4"
	_ "github.com/mattn/go-sqlite3" // SQLite3 driver
	kit "github.com/vctt94/bisonbotkit"
	"github.com/vctt94/bisonbotkit/config"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/bisonbotkit/utils"
	"github.com/vctt94/poker-bisonrelay/poker"
	"github.com/vctt94/poker-bisonrelay/rpc/grpc/pokerrpc"
	"github.com/vctt94/poker-bisonrelay/server"
	"google.golang.org/grpc"
)

var (
	flagAppRoot = flag.String("approot", "~/.pokerbot", "Path to application data directory")
)

// BotState holds the state of the poker bot
type BotState struct {
	db     server.Database
	tables map[string]*poker.Table
	mu     sync.RWMutex
}

// handlePM handles incoming PM commands.
func handlePM(ctx context.Context, bot *kit.Bot, pm *types.ReceivedPM, state *BotState) {
	tokens := strings.Fields(pm.Msg.Message)
	if len(tokens) == 0 {
		return
	}

	cmd := strings.ToLower(tokens[0])
	var uid zkidentity.ShortID
	uid.FromBytes(pm.Uid)
	playerID := uid.String()

	switch cmd {
	case "balance":
		balance, err := state.db.GetPlayerBalance(playerID)
		if err != nil {
			bot.SendPM(ctx, pm.Nick, "Error checking balance: "+err.Error())
			return
		}
		bot.SendPM(ctx, pm.Nick, fmt.Sprintf("Your current balance is: %.8f DCR",
			dcrutil.Amount(balance).ToCoin()))

	case "create":
		if len(tokens) < 2 {
			bot.SendPM(ctx, pm.Nick, "Usage: create <buy-in amount in DCR>")
			return
		}

		// Parse buy-in amount
		buyInFloat, err := strconv.ParseFloat(tokens[1], 64)
		if err != nil {
			bot.SendPM(ctx, pm.Nick, "Invalid buy-in amount. Please enter a valid number.")
			return
		}

		buyIn, err := dcrutil.NewAmount(buyInFloat)
		if err != nil {
			bot.SendPM(ctx, pm.Nick, "Invalid DCR amount. Please enter a valid number.")
			return
		}

		// Check player balance
		balance, err := state.db.GetPlayerBalance(playerID)
		if err != nil {
			bot.SendPM(ctx, pm.Nick, "Error checking balance: "+err.Error())
			return
		}

		if balance < int64(buyIn) {
			bot.SendPM(ctx, pm.Nick, "Insufficient balance for buy-in.")
			return
		}

		// Create new table
		tableID := fmt.Sprintf("table-%d", time.Now().Unix())
		table := poker.NewTable(poker.TableConfig{
			ID:         tableID,
			CreatorID:  playerID,
			BuyIn:      int64(buyIn),
			MinPlayers: 2,
			MaxPlayers: 6,
			SmallBlind: int64(buyIn) / 100, // 1% of buy-in
			BigBlind:   int64(buyIn) / 50,  // 2% of buy-in
			TimeBank:   30 * time.Second,
		})

		// Add creator to table
		err = table.AddPlayer(playerID, balance)
		if err != nil {
			bot.SendPM(ctx, pm.Nick, "Error creating table: "+err.Error())
			return
		}

		// Add table to state
		state.mu.Lock()
		state.tables[tableID] = table
		state.mu.Unlock()

		bot.SendPM(ctx, pm.Nick, fmt.Sprintf("Table %s created with buy-in of %.8f DCR. Use 'join %s' to join.",
			tableID, buyIn.ToCoin(), tableID))

	case "join":
		if len(tokens) < 2 {
			bot.SendPM(ctx, pm.Nick, "Usage: join <table-id>")
			return
		}

		tableID := tokens[1]
		state.mu.RLock()
		table, exists := state.tables[tableID]
		state.mu.RUnlock()

		if !exists {
			bot.SendPM(ctx, pm.Nick, "Table not found.")
			return
		}

		// Check player balance
		balance, err := state.db.GetPlayerBalance(playerID)
		if err != nil {
			bot.SendPM(ctx, pm.Nick, "Error checking balance: "+err.Error())
			return
		}

		// Add player to table
		err = table.AddPlayer(playerID, balance)
		if err != nil {
			bot.SendPM(ctx, pm.Nick, "Error joining table: "+err.Error())
			return
		}

		// Notify all players at the table
		players := table.GetPlayers()
		for _, p := range players {
			if p.ID != playerID {
				bot.SendPM(ctx, p.ID, fmt.Sprintf("%s has joined the table.", pm.Nick))
			}
		}

		bot.SendPM(ctx, pm.Nick, fmt.Sprintf("Joined table %s. Current status:\n%s",
			tableID, table.GetStatus()))

		// Check if we can start the game
		if len(players) >= table.GetMinPlayers() {
			err = table.StartGame()
			if err != nil {
				bot.SendPM(ctx, pm.Nick, "Error starting game: "+err.Error())
				return
			}

			// Notify all players
			for _, p := range players {
				bot.SendPM(ctx, p.ID, "Game started!")
			}
		}

	case "tables":
		state.mu.RLock()
		defer state.mu.RUnlock()

		if len(state.tables) == 0 {
			bot.SendPM(ctx, pm.Nick, "No active tables.")
			return
		}

		msg := "Active tables:\n"
		for id, table := range state.tables {
			msg += fmt.Sprintf("%s: %d/%d players\n", id, len(table.GetPlayers()), table.GetMaxPlayers())
		}
		bot.SendPM(ctx, pm.Nick, msg)

	case "help":
		helpMsg := `Available commands:
- balance: Check your current balance
- create <amount>: Create a new poker table with specified buy-in
- join <table-id>: Join an existing poker table
- tables: List all active tables
- help: Show this help message`
		bot.SendPM(ctx, pm.Nick, helpMsg)

	default:
		bot.SendPM(ctx, pm.Nick, "Unknown command. Type 'help' for available commands.")
	}
}

func realMain() error {
	flag.Parse()

	// Expand and clean the app root path
	appRoot := utils.CleanAndExpandPath(*flagAppRoot)

	// Ensure the log directory exists
	logDir := filepath.Join(appRoot, "logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return fmt.Errorf("failed to create log directory: %v", err)
	}

	// Initialize logging
	logBackend, err := logging.NewLogBackend(logging.LogConfig{
		LogFile:     filepath.Join(logDir, "pokerbot.log"),
		DebugLevel:  "info",
		MaxLogFiles: 5,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize logging: %v", err)
	}
	defer logBackend.Close()

	log := logBackend.Logger("PokerBot")

	// Load bot configuration
	cfg, err := config.LoadBotConfig(appRoot, "pokerbot.conf")
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}

	// Initialize database
	db, err := server.NewDatabase(filepath.Join(appRoot, "poker.db"))
	if err != nil {
		return fmt.Errorf("failed to initialize database: %v", err)
	}
	defer db.Close()

	// Initialize and start the gRPC poker server
	pokerServer := server.NewServer(db)
	grpcLis, err := net.Listen("tcp", ":50051")
	if err != nil {
		return fmt.Errorf("failed to listen for gRPC poker server: %v", err)
	}
	grpcServer := grpc.NewServer()
	pokerrpc.RegisterLobbyServiceServer(grpcServer, pokerServer)
	pokerrpc.RegisterPokerServiceServer(grpcServer, pokerServer)

	go func() {
		log.Infof("Starting gRPC poker server on :50051")
		if err := grpcServer.Serve(grpcLis); err != nil {
			log.Errorf("gRPC poker server error: %v", err)
		}
	}()
	defer grpcServer.Stop() // Ensure gRPC server is stopped on exit

	// Initialize bot state
	state := &BotState{
		db:     db,
		tables: make(map[string]*poker.Table),
	}

	// Create channels for handling PMs and tips
	pmChan := make(chan types.ReceivedPM)
	tipChan := make(chan types.ReceivedTip)
	tipProgressChan := make(chan types.TipProgressEvent)

	cfg.PMChan = pmChan
	cfg.PMLog = logBackend.Logger("PM")
	cfg.TipLog = logBackend.Logger("TIP")
	cfg.TipProgressChan = tipProgressChan
	cfg.TipReceivedLog = logBackend.Logger("TIP_RECEIVED")
	cfg.TipReceivedChan = tipChan

	log.Infof("Starting bot...")

	bot, err := kit.NewBot(cfg, logBackend)
	if err != nil {
		return fmt.Errorf("failed to create bot: %v", err)
	}

	// Set up context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle PMs
	go func() {
		for pm := range pmChan {
			handlePM(ctx, bot, &pm, state)
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
				bot.SendPM(ctx, userID.String(),
					"Error updating your balance. Please contact support.")
			} else {
				bot.SendPM(ctx, userID.String(),
					fmt.Sprintf("Thank you for the tip of %.8f DCR! Your balance has been updated.",
						dcrutil.Amount(tip.AmountMatoms/1e3).ToCoin()))
			}

			bot.AckTipReceived(ctx, tip.SequenceId)
		}
	}()

	// Handle tip progress updates
	go func() {
		for progress := range tipProgressChan {
			log.Infof("Tip progress event (sequence ID: %d)", progress.SequenceId)
			err := bot.AckTipProgress(ctx, progress.SequenceId)
			if err != nil {
				log.Errorf("Failed to acknowledge tip progress: %v", err)
			}
		}
	}()

	// Run the bot
	err = bot.Run(ctx)
	log.Infof("Bot exited: %v", err)
	return err
}

func main() {
	if err := realMain(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

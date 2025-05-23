package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/companyzero/bisonrelay/clientrpc/types"
	"github.com/companyzero/bisonrelay/zkidentity"
	"github.com/decred/dcrd/dcrutil/v4"
	kit "github.com/vctt94/bisonbotkit"
	"github.com/vctt94/poker-bisonrelay/pkg/poker"
	"github.com/vctt94/poker-bisonrelay/pkg/server"
)

// State holds the state of the poker bot
type State struct {
	db     server.Database
	tables map[string]*poker.Table
	mu     sync.RWMutex
}

// NewState creates a new bot state with the given database
func NewState(db server.Database) *State {
	return &State{
		db:     db,
		tables: make(map[string]*poker.Table),
	}
}

// HandlePM handles incoming PM commands.
func (s *State) HandlePM(ctx context.Context, bot *kit.Bot, pm *types.ReceivedPM) {
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
		balance, err := s.db.GetPlayerBalance(playerID)
		if err != nil {
			bot.SendPM(ctx, pm.Nick, "Error checking balance: "+err.Error())
			return
		}
		bot.SendPM(ctx, pm.Nick, fmt.Sprintf("Your current balance is: %.8f DCR",
			dcrutil.Amount(balance).ToCoin()))

	case "create":
		s.handleCreateTable(ctx, bot, pm, tokens, playerID)

	case "join":
		s.handleJoinTable(ctx, bot, pm, tokens, playerID)

	case "tables":
		s.handleListTables(ctx, bot, pm)

	case "help":
		s.handleHelp(ctx, bot, pm)

	default:
		bot.SendPM(ctx, pm.Nick, "Unknown command. Type 'help' for available commands.")
	}
}

func (s *State) handleCreateTable(ctx context.Context, bot *kit.Bot, pm *types.ReceivedPM, tokens []string, playerID string) {
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
	balance, err := s.db.GetPlayerBalance(playerID)
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
	s.mu.Lock()
	s.tables[tableID] = table
	s.mu.Unlock()

	bot.SendPM(ctx, pm.Nick, fmt.Sprintf("Table %s created with buy-in of %.8f DCR. Use 'join %s' to join.",
		tableID, buyIn.ToCoin(), tableID))
}

func (s *State) handleJoinTable(ctx context.Context, bot *kit.Bot, pm *types.ReceivedPM, tokens []string, playerID string) {
	if len(tokens) < 2 {
		bot.SendPM(ctx, pm.Nick, "Usage: join <table-id>")
		return
	}

	tableID := tokens[1]
	s.mu.RLock()
	table, exists := s.tables[tableID]
	s.mu.RUnlock()

	if !exists {
		bot.SendPM(ctx, pm.Nick, "Table not found.")
		return
	}

	// Check player balance
	balance, err := s.db.GetPlayerBalance(playerID)
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
}

func (s *State) handleListTables(ctx context.Context, bot *kit.Bot, pm *types.ReceivedPM) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.tables) == 0 {
		bot.SendPM(ctx, pm.Nick, "No active tables.")
		return
	}

	msg := "Active tables:\n"
	for id, table := range s.tables {
		msg += fmt.Sprintf("%s: %d/%d players\n", id, len(table.GetPlayers()), table.GetMaxPlayers())
	}
	bot.SendPM(ctx, pm.Nick, msg)
}

func (s *State) handleHelp(ctx context.Context, bot *kit.Bot, pm *types.ReceivedPM) {
	helpMsg := `Available commands:
- balance: Check your current balance
- create <amount>: Create a new poker table with specified buy-in
- join <table-id>: Join an existing poker table
- tables: List all active tables
- help: Show this help message`
	bot.SendPM(ctx, pm.Nick, helpMsg)
}

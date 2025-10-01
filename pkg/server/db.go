package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/vctt94/pokerbisonrelay/pkg/poker"
	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/pokerbisonrelay/pkg/server/internal/db"
)

// Database defines the interface for database operations
type Database interface {
	// GetPlayerBalance returns the current balance of a player
	GetPlayerBalance(playerID string) (int64, error)
	// UpdatePlayerBalance updates a player's balance and records the transaction
	UpdatePlayerBalance(playerID string, amount int64, transactionType, description string) error

	// Game state persistence
	SaveTableState(tableState *db.TableState) error
	// SaveSnapshot atomically persists a table state together with its related player states.
	SaveSnapshot(tableState *db.TableState, playerStates []*db.PlayerState) error
	LoadTableState(tableID string) (*db.TableState, error)
	DeleteTableState(tableID string) error

	// Player state at table
	SavePlayerState(tableID string, playerState *db.PlayerState) error
	LoadPlayerStates(tableID string) ([]*db.PlayerState, error)
	DeletePlayerState(tableID, playerID string) error

	// Table discovery
	GetAllTableIDs() ([]string, error)

	// Close closes the database connection
	Close() error
}

// Transaction represents a player's transaction
type Transaction struct {
	ID          int64
	PlayerID    string
	Amount      int64
	Type        string
	Description string
	CreatedAt   string
}

// NewDatabase creates a new database connection
func NewDatabase(dbPath string) (Database, error) {
	// Ensure the directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %v", err)
	}

	// Create the database
	return db.NewDB(dbPath)
}

// loadTableFromDatabase restores a table from the database
func (s *Server) loadTableFromDatabase(tableID string) (*poker.Table, error) {
	// Load table state
	dbTableState, err := s.db.LoadTableState(tableID)
	if err != nil {
		return nil, fmt.Errorf("failed to load table state: %v", err)
	}

	// Create table config
	// Use dedicated loggers (levels controlled by backend debug level)
	tblLog := s.logBackend.Logger("TABLE")
	gameLog := s.logBackend.Logger("GAME")

	cfg := poker.TableConfig{
		ID:             dbTableState.ID,
		Log:            tblLog,
		GameLog:        gameLog,
		HostID:         dbTableState.HostID,
		BuyIn:          dbTableState.BuyIn,
		MinPlayers:     dbTableState.MinPlayers,
		MaxPlayers:     dbTableState.MaxPlayers,
		SmallBlind:     dbTableState.SmallBlind,
		BigBlind:       dbTableState.BigBlind,
		MinBalance:     dbTableState.MinBalance,
		StartingChips:  dbTableState.StartingChips,
		TimeBank:       dbTableState.TimeBank,       // Default
		AutoStartDelay: dbTableState.AutoStartDelay, // Default
	}

	// Create table
	table := poker.NewTable(cfg)

	// Register the table early so that any asynchronous snapshot operations
	// triggered during restoration can successfully locate it.
	s.mu.Lock()
	s.tables[tableID] = table
	s.mu.Unlock()

	// Load player states
	dbPlayerStates, err := s.db.LoadPlayerStates(tableID)
	if err != nil {
		return nil, fmt.Errorf("failed to load player states: %v", err)
	}

	// Ensure deterministic order by sorting by seat before we recreate users. This guarantees
	// that the index-based CurrentPlayer value persisted in the snapshot correctly
	// references the same logical player once the game is restored.
	sort.Slice(dbPlayerStates, func(i, j int) bool {
		return dbPlayerStates[i].TableSeat < dbPlayerStates[j].TableSeat
	})

	// Restore users to table
	for _, dbPlayerState := range dbPlayerStates {
		user := s.restoreUserFromDB(dbPlayerState)

		// Add user back to table
		_, err := table.AddNewUser(user.ID, user.ID, user.DCRAccountBalance, user.TableSeat)
		if err != nil {
			s.log.Errorf("Failed to add restored user %s to table: %v", user.ID, err)
			continue
		}

		// Update user state from saved data
		restoredUser := table.GetUser(user.ID)
		if restoredUser != nil {
			s.applyUserState(restoredUser, dbPlayerState)
		}
	}

	// Restore game state if game was started
	if dbTableState.GameStarted {
		err := s.restoreGameState(table, dbTableState, dbPlayerStates)
		if err != nil {
			s.log.Errorf("Failed to restore game state for table %s: %v", tableID, err)
		} else {
			s.log.Infof("Successfully restored active game for table %s", tableID)
		}
	}

	return table, nil
}

// restoreUserFromState creates a user from saved state
func (s *Server) restoreUserFromDB(dbPlayerState *db.PlayerState) *poker.User {
	// Get the player's current DCR balance from the database
	dcrBalance, err := s.db.GetPlayerBalance(dbPlayerState.PlayerID)
	if err != nil {
		s.log.Errorf("Failed to get DCR balance for player %s: %v", dbPlayerState.PlayerID, err)
		dcrBalance = 0 // Default to 0 if we can't get the balance
	}

	user := poker.NewUser(dbPlayerState.PlayerID, dbPlayerState.PlayerID, dcrBalance, dbPlayerState.TableSeat)
	return user
}

// parseGamePhase converts a string game phase to the enum type
func (s *Server) parseGamePhase(phaseStr string) pokerrpc.GamePhase {
	switch phaseStr {
	case "WAITING":
		return pokerrpc.GamePhase_WAITING
	case "NEW_HAND_DEALING":
		return pokerrpc.GamePhase_NEW_HAND_DEALING
	case "PRE_FLOP":
		return pokerrpc.GamePhase_PRE_FLOP
	case "FLOP":
		return pokerrpc.GamePhase_FLOP
	case "TURN":
		return pokerrpc.GamePhase_TURN
	case "RIVER":
		return pokerrpc.GamePhase_RIVER
	case "SHOWDOWN":
		return pokerrpc.GamePhase_SHOWDOWN
	default:
		return pokerrpc.GamePhase_WAITING
	}
}

// restoreGameState restores an active game from database state
func (s *Server) restoreGameState(table *poker.Table, dbTableState *db.TableState, dbPlayerStates []*db.PlayerState) error {
	s.log.Infof("Restoring game state for table %s: phase=%s, dealer=%d, currentPlayer=%d",
		dbTableState.ID, dbTableState.GamePhase, dbTableState.Dealer, dbTableState.CurrentPlayer)

	// Build a fresh *poker.Game without triggering any hand setup logic. This
	// avoids posting blinds or dealing cards again during restoration.

	tblCfg := table.GetConfig()

	users := table.GetUsers()
	// Ensure stable ordering by seat so indices match persisted data.
	sort.Slice(users, func(i, j int) bool { return users[i].TableSeat < users[j].TableSeat })

	gameLog := s.logBackend.Logger("GAME")
	gCfg := poker.GameConfig{
		NumPlayers:     len(users),
		StartingChips:  tblCfg.StartingChips,
		SmallBlind:     tblCfg.SmallBlind,
		BigBlind:       tblCfg.BigBlind,
		TimeBank:       tblCfg.TimeBank,
		AutoStartDelay: tblCfg.AutoStartDelay,
		Log:            gameLog,
	}

	game, err := poker.NewGame(gCfg)
	if err != nil {
		return fmt.Errorf("failed to create game during restoration: %v", err)
	}

	// Populate game players from table users (creates fresh *Player objects).
	game.SetPlayers(users)

	// Inject the reconstructed game into the table (sets state to GAME_ACTIVE).
	table.RestoreGame(game)

	// Restore community cards
	if dbTableState.CommunityCards != nil {
		if communityCardsJSON, ok := dbTableState.CommunityCards.(string); ok && communityCardsJSON != "" && communityCardsJSON != "[]" {
			var communityCards []poker.Card
			if err := json.Unmarshal([]byte(communityCardsJSON), &communityCards); err == nil {
				game.SetCommunityCards(communityCards)
				s.log.Debugf("Restored %d community cards", len(communityCards))
			}
		}
	}

	// Restore game-level state using the SetGameState method
	gamePhase := s.parseGamePhase(dbTableState.GamePhase)
	game.SetGameState(
		dbTableState.Dealer,
		dbTableState.CurrentPlayer,
		dbTableState.Round,
		dbTableState.BetRound,
		dbTableState.CurrentBet,
		dbTableState.Pot,
		gamePhase,
	)

	// Restore player state from database, including hands
	game.ModifyPlayers(func(players []*poker.Player) {
		for _, dbPlayerState := range dbPlayerStates {
			for _, player := range players {
				if player.ID != dbPlayerState.PlayerID {
					continue
				}

				// Restore game state fields
				player.Balance = dbPlayerState.Balance
				player.StartingBalance = dbPlayerState.StartingBalance
				player.CurrentBet = dbPlayerState.CurrentBet
				player.IsDealer = dbPlayerState.IsDealer
				player.IsTurn = dbPlayerState.IsTurn
				player.HandDescription = dbPlayerState.HandDescription
				player.SetGameState(dbPlayerState.GameState)

				// Restore hand cards
				if dbPlayerState.Hand != nil {
					if handJSON, ok := dbPlayerState.Hand.(string); ok && handJSON != "" && handJSON != "[]" {
						var cards []poker.Card
						if err := json.Unmarshal([]byte(handJSON), &cards); err == nil {
							player.Hand = cards
							s.log.Debugf("Restored %d cards for player %s", len(cards), player.ID)
						} else {
							s.log.Errorf("Failed to unmarshal hand for player %s: %v", player.ID, err)
						}
					}
				}

				// Set table-level state
				player.TableSeat = dbPlayerState.TableSeat
				player.IsReady = dbPlayerState.IsReady

				s.log.Debugf("Restored player %s: balance=%d, hasbet=%d,  disconnected=%v",
					player.ID, player.Balance, player.CurrentBet, player.IsDisconnected)

				break
			}
		}
	})

	// Reconstruct pot based on each player's saved bet so that GetPot() matches
	// the persisted total. We do this outside the ModifyPlayers block to avoid
	// holding the game write-lock for the additional potManager updates.
	for idx, p := range game.GetPlayers() {
		if p.CurrentBet > 0 {
			game.AddToPotForPlayer(idx, p.CurrentBet)
		}
	}

	// Ensure the pot total matches the snapshot exactly (bets alone may not
	// capture contributions from previous betting rounds).
	game.ForceSetPot(dbTableState.Pot)

	s.log.Infof("Successfully restored game state: dealer=%d, currentPlayer=%d, pot=%d, phase=%s, players=%d",
		dbTableState.Dealer, dbTableState.CurrentPlayer, dbTableState.Pot, dbTableState.GamePhase, len(game.GetPlayers()))

	return nil
}

// loadAllTables loads all persisted tables from the database on server startup
func (s *Server) loadAllTables() error {
	s.log.Infof("Loading persisted tables from database...")

	// Get all table IDs from the database
	tableIDs, err := s.db.GetAllTableIDs()
	if err != nil {
		return fmt.Errorf("failed to get table IDs from database: %v", err)
	}

	if len(tableIDs) == 0 {
		s.log.Infof("No persisted tables found in database")
		return nil
	}

	loadedCount := 0
	for _, tableID := range tableIDs {
		table, err := s.loadTableFromDatabase(tableID)
		if err != nil {
			s.log.Errorf("Failed to load table %s: %v", tableID, err)
			continue
		}

		s.mu.Lock()
		s.tables[tableID] = table
		s.mu.Unlock()

		loadedCount++
		s.log.Infof("Loaded table %s from database", tableID)
	}

	s.log.Infof("Successfully loaded %d of %d persisted tables", loadedCount, len(tableIDs))
	return nil
}

// applyUserState applies saved player state to a restored user
func (s *Server) applyUserState(user *poker.User, dbPlayerState *db.PlayerState) {
	// Apply table-level state
	user.IsReady = dbPlayerState.IsReady

	// Note: TableSeat should already be set correctly when user was created from state
	// but ensure it matches the saved state
	user.TableSeat = dbPlayerState.TableSeat

	s.log.Debugf("Applied user state for player %s: ready=%v, seat=%d",
		user.ID, user.IsReady, user.TableSeat)
}

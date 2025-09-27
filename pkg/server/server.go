package server

import (
	"encoding/json"
	"fmt"
	"sort" // ensure deterministic ordering when (de)serializing player slices
	"sync"

	"github.com/decred/slog"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/pokerbisonrelay/pkg/poker"
	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/pokerbisonrelay/pkg/server/internal/db"
)

// NotificationStream represents a client's notification stream
type NotificationStream struct {
	playerID string
	stream   pokerrpc.LobbyService_StartNotificationStreamServer
	done     chan struct{}
}

// Server implements both PokerService and LobbyService
type Server struct {
	pokerrpc.UnimplementedPokerServiceServer
	pokerrpc.UnimplementedLobbyServiceServer
	log        slog.Logger
	logBackend *logging.LogBackend
	db         Database
	tables     map[string]*poker.Table
	mu         sync.RWMutex

	// Notification streaming
	notificationStreams map[string]*NotificationStream
	notificationMu      sync.RWMutex

	// Game streaming
	gameStreams   map[string]map[string]pokerrpc.PokerService_StartGameStreamServer // tableID -> playerID -> stream
	gameStreamsMu sync.RWMutex

	// Table state saving synchronization
	saveMutexes map[string]*sync.Mutex // tableID -> mutex for that table's saves
	saveMu      sync.RWMutex           // protects saveMutexes map

	// WaitGroup to ensure all async save goroutines complete before Shutdown
	saveWg sync.WaitGroup

	// Event-driven architecture components
	eventProcessor *EventProcessor
}

// NewServer creates a new poker server
func NewServer(db Database, logBackend *logging.LogBackend) *Server {
	server := &Server{
		log:                 logBackend.Logger("SERVER"),
		logBackend:          logBackend,
		db:                  db,
		tables:              make(map[string]*poker.Table),
		notificationStreams: make(map[string]*NotificationStream),
		gameStreams:         make(map[string]map[string]pokerrpc.PokerService_StartGameStreamServer),
		saveMutexes:         make(map[string]*sync.Mutex),
	}

	// Initialize event processor for deadlock-free architecture
	server.eventProcessor = NewEventProcessor(server, 1000, 3) // queue size: 1000, workers: 3
	server.eventProcessor.Start()

	// Load persisted tables on startup
	err := server.loadAllTables()
	if err != nil {
		server.log.Errorf("Failed to load persisted tables: %v", err)
	}

	return server
}

// Stop gracefully stops the server
func (s *Server) Stop() {
	if s.eventProcessor != nil {
		s.eventProcessor.Stop()
	}
	// Wait for any in-flight asynchronous saves to complete before returning.
	s.saveWg.Wait()
}

// SaveTableStateAsync implements the StateSaver interface for tables
func (s *Server) SaveTableStateAsync(tableID string, reason string) {
	s.saveTableStateAsync(tableID, reason)
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
	table.SetStateSaver(s)

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
		user := s.restoreUserFromState(dbPlayerState)

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
func (s *Server) restoreUserFromState(dbPlayerState *db.PlayerState) *poker.User {
	// Get the player's current DCR balance from the database
	dcrBalance, err := s.db.GetPlayerBalance(dbPlayerState.PlayerID)
	if err != nil {
		s.log.Errorf("Failed to get DCR balance for player %s: %v", dbPlayerState.PlayerID, err)
		dcrBalance = 0 // Default to 0 if we can't get the balance
	}

	user := poker.NewUser(dbPlayerState.PlayerID, dbPlayerState.PlayerID, dcrBalance, dbPlayerState.TableSeat)
	return user
}

// transferTableHost transfers host ownership to a new user
func (s *Server) transferTableHost(tableID, newHostID string) error {
	table, ok := s.tables[tableID]
	if !ok {
		return fmt.Errorf("table not found")
	}

	// Use the table's SetHost method to transfer ownership
	err := table.SetHost(newHostID)
	if err != nil {
		return fmt.Errorf("failed to transfer host: %v", err)
	}

	s.log.Infof("Host transferred to %s for table %s", newHostID, tableID)

	return nil
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
				player.HasBet = dbPlayerState.HasBet
				player.HasFolded = dbPlayerState.HasFolded
				player.IsAllIn = dbPlayerState.IsAllIn
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

				s.log.Debugf("Restored player %s: balance=%d, hasbet=%d, folded=%v, disconnected=%v",
					player.ID, player.Balance, player.HasBet, player.HasFolded, player.IsDisconnected)

				break
			}
		}
	})

	// Reconstruct pot based on each player's saved bet so that GetPot() matches
	// the persisted total. We do this outside the ModifyPlayers block to avoid
	// holding the game write-lock for the additional potManager updates.
	for idx, p := range game.GetPlayers() {
		if p.HasBet > 0 {
			game.AddToPotForPlayer(idx, p.HasBet)
		}
	}

	// Ensure the pot total matches the snapshot exactly (bets alone may not
	// capture contributions from previous betting rounds).
	game.ForceSetPot(dbTableState.Pot)

	s.log.Infof("Successfully restored game state: dealer=%d, currentPlayer=%d, pot=%d, phase=%s, players=%d",
		dbTableState.Dealer, dbTableState.CurrentPlayer, dbTableState.Pot, dbTableState.GamePhase, len(game.GetPlayers()))

	return nil
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

// markPlayerDisconnected marks a player as disconnected but keeps them in the game
func (s *Server) markPlayerDisconnected(tableID, playerID string) error {
	s.mu.Lock()
	table, ok := s.tables[tableID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("table not found")
	}
	user := table.GetUser(playerID)
	if user == nil {
		s.mu.Unlock()
		return fmt.Errorf("player not found at table")
	}
	user.IsDisconnected = true
	s.mu.Unlock()

	// Persist table snapshot asynchronously so flag is saved in memory snapshot only.
	s.saveTableStateAsync(tableID, "player disconnected")
	s.log.Infof("Player %s marked as disconnected from table %s", playerID, tableID)
	return nil
}

// markPlayerConnected marks a player as connected
func (s *Server) markPlayerConnected(tableID, playerID string) error {
	s.mu.Lock()
	table, ok := s.tables[tableID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("table not found")
	}
	user := table.GetUser(playerID)
	if user == nil {
		s.mu.Unlock()
		return fmt.Errorf("player not found at table")
	}
	user.IsDisconnected = false
	s.mu.Unlock()

	s.saveTableStateAsync(tableID, "player reconnected")
	s.log.Infof("Player %s marked as connected to table %s", playerID, tableID)
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

// cleanupDisconnectedPlayers removes players who have been disconnected too long or have no chips
func (s *Server) cleanupDisconnectedPlayers() {
	s.log.Debugf("Running disconnected player cleanup...")

	s.mu.RLock()
	tableIDs := make([]string, 0, len(s.tables))
	for tableID := range s.tables {
		tableIDs = append(tableIDs, tableID)
	}
	s.mu.RUnlock()

	for _, tableID := range tableIDs {
		s.cleanupDisconnectedPlayersForTable(tableID)
	}
}

// cleanupDisconnectedPlayersForTable cleans up disconnected players for a specific table
func (s *Server) cleanupDisconnectedPlayersForTable(tableID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	table, ok := s.tables[tableID]
	if !ok {
		return
	}

	playersToRemove := []string{}

	for _, user := range table.GetUsers() {
		if !user.IsDisconnected {
			continue
		}
		// Determine chip balance: if game active, look at game player; else 0 chips triggers removal.
		chipBalance := int64(0)
		if table.IsGameStarted() && table.GetGame() != nil {
			for _, gp := range table.GetGame().GetPlayers() {
				if gp.ID == user.ID {
					chipBalance = gp.Balance
					break
				}
			}
		}
		if chipBalance == 0 {
			playersToRemove = append(playersToRemove, user.ID)
			s.log.Infof("Marking disconnected player %s with 0 chips for removal", user.ID)
		}
		// TODO: time-based cleanup as before
	}

	for _, pid := range playersToRemove {
		_ = table.RemoveUser(pid)
		_ = s.db.DeletePlayerState(tableID, pid)
	}

	if len(playersToRemove) > 0 {
		s.saveTableStateAsync(tableID, "disconnected player cleanup")
	}
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

package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/poker-bisonrelay/pkg/server/internal/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestServer implements the PokerServiceServer interface
type TestServer struct {
	*Server
}

// InMemoryDB implements Database interface for testing
type InMemoryDB struct {
	balances            map[string]int64
	transactions        map[string][]Transaction
	tableStates         map[string]*db.TableState
	playerStates        map[string]map[string]*db.PlayerState // tableID -> playerID -> PlayerState
	disconnectedPlayers map[string]map[string]bool            // tableID -> playerID -> isDisconnected
}

// NewInMemoryDB creates a new in-memory database for testing
func NewInMemoryDB() *InMemoryDB {
	return &InMemoryDB{
		balances:            make(map[string]int64),
		transactions:        make(map[string][]Transaction),
		tableStates:         make(map[string]*db.TableState),
		playerStates:        make(map[string]map[string]*db.PlayerState),
		disconnectedPlayers: make(map[string]map[string]bool),
	}
}

// GetPlayerBalance returns the current balance of a player
func (m *InMemoryDB) GetPlayerBalance(playerID string) (int64, error) {
	balance, exists := m.balances[playerID]
	if !exists {
		return 0, fmt.Errorf("player not found")
	}
	return balance, nil
}

// UpdatePlayerBalance updates a player's balance and records the transaction
func (m *InMemoryDB) UpdatePlayerBalance(playerID string, amount int64, transactionType, description string) error {
	m.balances[playerID] += amount

	// Record transaction
	tx := Transaction{
		ID:          int64(len(m.transactions[playerID]) + 1),
		PlayerID:    playerID,
		Amount:      amount,
		Type:        transactionType,
		Description: description,
		CreatedAt:   time.Now().Format(time.RFC3339),
	}
	m.transactions[playerID] = append(m.transactions[playerID], tx)

	return nil
}

// GetPlayerTransactions returns the transaction history for a player
func (m *InMemoryDB) GetPlayerTransactions(playerID string, limit int) ([]Transaction, error) {
	transactions := m.transactions[playerID]
	if limit > 0 && limit < len(transactions) {
		return transactions[:limit], nil
	}
	return transactions, nil
}

// SaveTableState saves table state to memory
func (m *InMemoryDB) SaveTableState(tableState *db.TableState) error {
	m.tableStates[tableState.ID] = tableState
	return nil
}

// LoadTableState loads table state from memory
func (m *InMemoryDB) LoadTableState(tableID string) (*db.TableState, error) {
	state, exists := m.tableStates[tableID]
	if !exists {
		return nil, fmt.Errorf("table state not found")
	}
	return state, nil
}

// DeleteTableState deletes table state from memory
func (m *InMemoryDB) DeleteTableState(tableID string) error {
	delete(m.tableStates, tableID)
	delete(m.playerStates, tableID)
	delete(m.disconnectedPlayers, tableID)
	return nil
}

// SavePlayerState saves player state to memory
func (m *InMemoryDB) SavePlayerState(tableID string, playerState *db.PlayerState) error {
	if m.playerStates[tableID] == nil {
		m.playerStates[tableID] = make(map[string]*db.PlayerState)
	}
	m.playerStates[tableID][playerState.PlayerID] = playerState
	return nil
}

// LoadPlayerStates loads all player states for a table from memory
func (m *InMemoryDB) LoadPlayerStates(tableID string) ([]*db.PlayerState, error) {
	tablePlayerStates := m.playerStates[tableID]
	if tablePlayerStates == nil {
		return []*db.PlayerState{}, nil
	}

	states := make([]*db.PlayerState, 0, len(tablePlayerStates))
	for _, state := range tablePlayerStates {
		states = append(states, state)
	}
	return states, nil
}

// DeletePlayerState deletes player state from memory
func (m *InMemoryDB) DeletePlayerState(tableID, playerID string) error {
	if m.playerStates[tableID] != nil {
		delete(m.playerStates[tableID], playerID)
	}
	if m.disconnectedPlayers[tableID] != nil {
		delete(m.disconnectedPlayers[tableID], playerID)
	}
	return nil
}

// SetPlayerDisconnected marks a player as disconnected
func (m *InMemoryDB) SetPlayerDisconnected(tableID, playerID string) error {
	if m.disconnectedPlayers[tableID] == nil {
		m.disconnectedPlayers[tableID] = make(map[string]bool)
	}
	m.disconnectedPlayers[tableID][playerID] = true
	return nil
}

// SetPlayerConnected marks a player as connected
func (m *InMemoryDB) SetPlayerConnected(tableID, playerID string) error {
	if m.disconnectedPlayers[tableID] == nil {
		m.disconnectedPlayers[tableID] = make(map[string]bool)
	}
	m.disconnectedPlayers[tableID][playerID] = false
	return nil
}

// IsPlayerDisconnected checks if a player is disconnected
func (m *InMemoryDB) IsPlayerDisconnected(tableID, playerID string) (bool, error) {
	if m.disconnectedPlayers[tableID] == nil {
		return false, fmt.Errorf("player state not found")
	}
	isDisconnected, exists := m.disconnectedPlayers[tableID][playerID]
	if !exists {
		return false, fmt.Errorf("player state not found")
	}
	return isDisconnected, nil
}

// GetAllTableIDs returns all table IDs
func (m *InMemoryDB) GetAllTableIDs() ([]string, error) {
	tableIDs := make([]string, 0, len(m.tableStates))
	for tableID := range m.tableStates {
		tableIDs = append(tableIDs, tableID)
	}
	return tableIDs, nil
}

// Close closes the database connection
func (m *InMemoryDB) Close() error {
	return nil
}

// createTestLogBackend creates a LogBackend for testing
func createTestLogBackend() *logging.LogBackend {
	logBackend, err := logging.NewLogBackend(logging.LogConfig{
		LogFile:        "",      // Empty for testing - will use stdout
		DebugLevel:     "error", // Set to error to reduce test output
		MaxLogFiles:    1,
		MaxBufferLines: 100,
	})
	if err != nil {
		// Fallback to a minimal LogBackend if creation fails
		return &logging.LogBackend{}
	}
	return logBackend
}

func TestPokerService(t *testing.T) {
	t.Run("GetBalance", func(t *testing.T) {
		// Create isolated database and server for this test
		db := NewInMemoryDB()
		defer db.Close()

		logBackend := createTestLogBackend()
		defer logBackend.Close()

		server := &TestServer{
			Server: NewServer(db, logBackend),
		}

		ctx := context.Background()
		playerID := "player1"

		// Test non-existent player
		_, err := server.GetBalance(ctx, &pokerrpc.GetBalanceRequest{PlayerId: "non-existent"})
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
		assert.Contains(t, st.Message(), "player not found")

		// Create player first
		_, err = server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    playerID,
			Amount:      0,
			Description: "initial balance",
		})
		require.NoError(t, err)

		// Test existing player
		resp, err := server.GetBalance(ctx, &pokerrpc.GetBalanceRequest{PlayerId: playerID})
		require.NoError(t, err)
		assert.Equal(t, int64(0), resp.Balance)
	})

	t.Run("UpdateBalance", func(t *testing.T) {
		// Create isolated database and server for this test
		db := NewInMemoryDB()
		defer db.Close()

		logBackend := createTestLogBackend()
		defer logBackend.Close()

		server := &TestServer{
			Server: NewServer(db, logBackend),
		}

		ctx := context.Background()
		playerID := "player1"

		// Test deposit
		resp, err := server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    playerID,
			Amount:      1000,
			Description: "initial deposit",
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1000), resp.NewBalance)

		// Test withdrawal
		resp, err = server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    playerID,
			Amount:      -500,
			Description: "withdrawal",
		})
		require.NoError(t, err)
		assert.Equal(t, int64(500), resp.NewBalance)
	})

	t.Run("CreateTable", func(t *testing.T) {
		// Create isolated database and server for this test
		db := NewInMemoryDB()
		defer db.Close()

		logBackend := createTestLogBackend()
		defer logBackend.Close()

		server := &TestServer{
			Server: NewServer(db, logBackend),
		}

		ctx := context.Background()
		player1ID := "player1"
		player2ID := "player2"

		// Set up initial balances
		_, err := server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    player1ID,
			Amount:      2500,
			Description: "initial deposit",
		})
		require.NoError(t, err)

		_, err = server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    player2ID,
			Amount:      1000,
			Description: "initial deposit",
		})
		require.NoError(t, err)

		// Test successful table creation
		resp, err := server.CreateTable(ctx, &pokerrpc.CreateTableRequest{
			PlayerId:      player1ID,
			SmallBlind:    10,
			BigBlind:      20,
			MinPlayers:    2,
			MaxPlayers:    6,
			BuyIn:         1000,
			StartingChips: 1000,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, resp.TableId)
		tableID := resp.TableId

		// Player2 joins the table
		joinResp, err := server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
			PlayerId: player2ID,
			TableId:  tableID,
		})
		require.NoError(t, err)
		require.True(t, joinResp.Success)
	})

	t.Run("GetGameState", func(t *testing.T) {
		// Create isolated database and server for this test
		db := NewInMemoryDB()
		defer db.Close()

		logBackend := createTestLogBackend()
		defer logBackend.Close()

		server := &TestServer{
			Server: NewServer(db, logBackend),
		}

		ctx := context.Background()

		// Test non-existent table
		_, err := server.GetGameState(ctx, &pokerrpc.GetGameStateRequest{
			TableId: "non-existent",
		})
		assert.Error(t, err)
	})

	t.Run("MakeBet", func(t *testing.T) {
		// Create isolated database and server for this test
		db := NewInMemoryDB()
		defer db.Close()

		logBackend := createTestLogBackend()
		defer logBackend.Close()

		server := &TestServer{
			Server: NewServer(db, logBackend),
		}

		ctx := context.Background()
		player1ID := "player1"
		player2ID := "player2"

		// Set up initial balances
		_, err := server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    player1ID,
			Amount:      2500,
			Description: "initial deposit",
		})
		require.NoError(t, err)

		_, err = server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    player2ID,
			Amount:      1000,
			Description: "initial deposit",
		})
		require.NoError(t, err)

		// Create table
		createResp, err := server.CreateTable(ctx, &pokerrpc.CreateTableRequest{
			PlayerId:      player1ID,
			SmallBlind:    10,
			BigBlind:      20,
			MinPlayers:    2,
			MaxPlayers:    6,
			BuyIn:         1000,
			StartingChips: 1000,
		})
		require.NoError(t, err)
		tableID := createResp.TableId

		// Player2 joins the table
		_, err = server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
			PlayerId: player2ID,
			TableId:  tableID,
		})
		require.NoError(t, err)

		// Both players set ready
		_, err = server.SetPlayerReady(ctx, &pokerrpc.SetPlayerReadyRequest{
			PlayerId: player1ID,
			TableId:  tableID,
		})
		require.NoError(t, err)

		_, err = server.SetPlayerReady(ctx, &pokerrpc.SetPlayerReadyRequest{
			PlayerId: player2ID,
			TableId:  tableID,
		})
		require.NoError(t, err)

		// Wait for game to start
		var gameStarted bool
		for i := 0; i < 10; i++ {
			time.Sleep(10 * time.Millisecond)
			gameState, err := server.GetGameState(ctx, &pokerrpc.GetGameStateRequest{
				TableId: tableID,
			})
			require.NoError(t, err)
			if gameState.GameState.GameStarted {
				gameStarted = true
				break
			}
		}
		require.True(t, gameStarted, "game should have started after both players are ready")

		// Get game state to find current player
		gameState, err := server.GetGameState(ctx, &pokerrpc.GetGameStateRequest{
			TableId: tableID,
		})
		require.NoError(t, err)
		currentPlayer := gameState.GameState.CurrentPlayer
		require.NotEmpty(t, currentPlayer, "there should be a current player to act")

		// Test successful bet with the current player
		resp, err := server.MakeBet(ctx, &pokerrpc.MakeBetRequest{
			PlayerId: currentPlayer,
			TableId:  tableID,
			Amount:   20,
		})
		require.NoError(t, err)
		assert.True(t, resp.Success)
	})
}

func TestPokerGameFlow(t *testing.T) {
	// Create isolated database and server for this test
	db := NewInMemoryDB()
	defer db.Close()

	logBackend := createTestLogBackend()
	defer logBackend.Close()

	server := &TestServer{
		Server: NewServer(db, logBackend),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Players
	alice := "alice"
	bob := "bob"
	charlie := "charlie"

	// Give players initial balance
	for _, player := range []string{alice, bob, charlie} {
		_, err := server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    player,
			Amount:      5000,
			Description: "initial balance",
		})
		require.NoError(t, err)
	}

	// Alice creates a table
	createResp, err := server.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:      alice,
		SmallBlind:    5,
		BigBlind:      10,
		MinPlayers:    3,
		MaxPlayers:    6,
		BuyIn:         100,
		StartingChips: 1000,
	})
	require.NoError(t, err)
	tableID := createResp.TableId

	// Bob joins
	joinResp, err := server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
		PlayerId: bob,
		TableId:  tableID,
	})
	require.NoError(t, err)
	assert.True(t, joinResp.Success)

	// Charlie joins
	joinResp, err = server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
		PlayerId: charlie,
		TableId:  tableID,
	})
	require.NoError(t, err)
	assert.True(t, joinResp.Success)

	// All players set ready
	for _, player := range []string{alice, bob, charlie} {
		_, err := server.SetPlayerReady(ctx, &pokerrpc.SetPlayerReadyRequest{
			PlayerId: player,
			TableId:  tableID,
		})
		require.NoError(t, err)
	}

	// Wait for game to start with timeout
	var gameStarted bool
	for i := 0; i < 20; i++ {
		select {
		case <-ctx.Done():
			t.Fatal("Test timed out waiting for game to start")
		default:
		}

		time.Sleep(50 * time.Millisecond)
		gameState, err := server.GetGameState(ctx, &pokerrpc.GetGameStateRequest{
			TableId: tableID,
		})
		require.NoError(t, err)
		if gameState.GameState.GameStarted {
			gameStarted = true
			break
		}
	}
	require.True(t, gameStarted, "game should have started")

	// Verify game state
	gameState, err := server.GetGameState(ctx, &pokerrpc.GetGameStateRequest{
		TableId: tableID,
	})
	require.NoError(t, err)
	assert.True(t, gameState.GameState.GameStarted)
	assert.Len(t, gameState.GameState.Players, 3)
}

func TestHostLeavesTableTransfersHost(t *testing.T) {
	// Create isolated database and server for this test
	db := NewInMemoryDB()
	defer db.Close()

	logBackend := createTestLogBackend()
	defer logBackend.Close()

	server := &TestServer{
		Server: NewServer(db, logBackend),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	host := "host"
	player := "player"

	// Give players initial balance
	for _, p := range []string{host, player} {
		_, err := server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    p,
			Amount:      5000,
			Description: "initial balance",
		})
		require.NoError(t, err)
	}

	// Host creates table
	createResp, err := server.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:      host,
		SmallBlind:    5,
		BigBlind:      10,
		MinPlayers:    2,
		MaxPlayers:    6,
		BuyIn:         100,
		StartingChips: 1000,
	})
	require.NoError(t, err)
	tableID := createResp.TableId

	// Player joins
	joinResp, err := server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
		PlayerId: player,
		TableId:  tableID,
	})
	require.NoError(t, err)
	assert.True(t, joinResp.Success)

	// Host leaves table
	leaveResp, err := server.LeaveTable(ctx, &pokerrpc.LeaveTableRequest{
		PlayerId: host,
		TableId:  tableID,
	})
	require.NoError(t, err)
	assert.True(t, leaveResp.Success)
	assert.Contains(t, leaveResp.Message, "Host transferred")

	// Verify table still exists and player is now host
	tablesResp, err := server.GetTables(ctx, &pokerrpc.GetTablesRequest{})
	require.NoError(t, err)
	assert.Len(t, tablesResp.Tables, 1)
	assert.Equal(t, player, tablesResp.Tables[0].HostId)
}

func TestLastPlayerLeavesTableClosure(t *testing.T) {
	// Create isolated database and server for this test
	db := NewInMemoryDB()
	defer db.Close()

	logBackend := createTestLogBackend()
	defer logBackend.Close()

	server := &TestServer{
		Server: NewServer(db, logBackend),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	host := "host"

	// Give host initial balance
	_, err := server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
		PlayerId:    host,
		Amount:      5000,
		Description: "initial balance",
	})
	require.NoError(t, err)

	// Host creates table
	createResp, err := server.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:      host,
		SmallBlind:    5,
		BigBlind:      10,
		MinPlayers:    2,
		MaxPlayers:    6,
		BuyIn:         100,
		StartingChips: 1000,
	})
	require.NoError(t, err)
	tableID := createResp.TableId

	// Verify table exists
	tablesResp, err := server.GetTables(ctx, &pokerrpc.GetTablesRequest{})
	require.NoError(t, err)
	assert.Len(t, tablesResp.Tables, 1)

	// Host leaves table (last player)
	leaveResp, err := server.LeaveTable(ctx, &pokerrpc.LeaveTableRequest{
		PlayerId: host,
		TableId:  tableID,
	})
	require.NoError(t, err)
	assert.True(t, leaveResp.Success)
	assert.Contains(t, leaveResp.Message, "table closed")

	// Verify table is removed
	tablesResp, err = server.GetTables(ctx, &pokerrpc.GetTablesRequest{})
	require.NoError(t, err)
	assert.Len(t, tablesResp.Tables, 0)
}

func TestNonHostLeavesTable(t *testing.T) {
	// Create isolated database and server for this test
	db := NewInMemoryDB()
	defer db.Close()

	logBackend := createTestLogBackend()
	defer logBackend.Close()

	server := &TestServer{
		Server: NewServer(db, logBackend),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	host := "host"
	player := "player"

	// Give players initial balance
	for _, p := range []string{host, player} {
		_, err := server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    p,
			Amount:      5000,
			Description: "initial balance",
		})
		require.NoError(t, err)
	}

	// Host creates table
	createResp, err := server.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:      host,
		SmallBlind:    5,
		BigBlind:      10,
		MinPlayers:    2,
		MaxPlayers:    6,
		BuyIn:         100,
		StartingChips: 1000,
	})
	require.NoError(t, err)
	tableID := createResp.TableId

	// Player joins
	joinResp, err := server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
		PlayerId: player,
		TableId:  tableID,
	})
	require.NoError(t, err)
	assert.True(t, joinResp.Success)

	// Player leaves table
	leaveResp, err := server.LeaveTable(ctx, &pokerrpc.LeaveTableRequest{
		PlayerId: player,
		TableId:  tableID,
	})
	require.NoError(t, err)
	assert.True(t, leaveResp.Success)

	// Verify table still exists and host is unchanged
	tablesResp, err := server.GetTables(ctx, &pokerrpc.GetTablesRequest{})
	require.NoError(t, err)
	assert.Len(t, tablesResp.Tables, 1)
	assert.Equal(t, host, tablesResp.Tables[0].HostId)
	assert.Equal(t, int32(1), tablesResp.Tables[0].CurrentPlayers, "Table should have 1 player remaining")
}

func TestLeaveTable(t *testing.T) {
	// Create isolated database and server for this test
	db := NewInMemoryDB()
	defer db.Close()

	logBackend := createTestLogBackend()
	defer logBackend.Close()

	server := &TestServer{
		Server: NewServer(db, logBackend),
	}

	ctx := context.Background()
	player1ID := "player1"

	// Test non-existent table
	resp, err := server.LeaveTable(ctx, &pokerrpc.LeaveTableRequest{
		PlayerId: player1ID,
		TableId:  "non-existent",
	})
	require.NoError(t, err)
	assert.False(t, resp.Success)
}

func TestJoinTable(t *testing.T) {
	// Create isolated database and server for this test
	db := NewInMemoryDB()
	defer db.Close()

	logBackend := createTestLogBackend()
	defer logBackend.Close()

	server := &TestServer{
		Server: NewServer(db, logBackend),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	player1ID := "player1"
	player2ID := "player2"

	// Set up initial balances
	_, err := server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
		PlayerId:    player1ID,
		Amount:      2500,
		Description: "initial deposit",
	})
	require.NoError(t, err)

	_, err = server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
		PlayerId:    player2ID,
		Amount:      1000,
		Description: "initial deposit",
	})
	require.NoError(t, err)

	// Create table
	createResp, err := server.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:      player1ID,
		SmallBlind:    10,
		BigBlind:      20,
		MinPlayers:    2,
		MaxPlayers:    6,
		BuyIn:         1000,
		StartingChips: 1000,
	})
	require.NoError(t, err)
	tableID := createResp.TableId

	// Test joining non-existent table
	resp, err := server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
		PlayerId: player2ID,
		TableId:  "non-existent",
	})
	require.NoError(t, err)
	assert.False(t, resp.Success)

	// Test successful join
	resp, err = server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
		PlayerId: player2ID,
		TableId:  tableID,
	})
	require.NoError(t, err)
	assert.True(t, resp.Success)

	// Test rejoining (this was causing deadlock before fix)
	resp, err = server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
		PlayerId: player2ID,
		TableId:  tableID,
	})
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Message, "Reconnected")

	// Test joining with insufficient balance
	player3ID := "player3"
	_, err = server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
		PlayerId:    player3ID,
		Amount:      500, // Not enough for 1000 buy-in
		Description: "insufficient balance",
	})
	require.NoError(t, err)

	resp, err = server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
		PlayerId: player3ID,
		TableId:  tableID,
	})
	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, "Insufficient DCR balance")
}

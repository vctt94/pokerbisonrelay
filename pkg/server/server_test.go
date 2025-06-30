package server

import (
	"context"
	"fmt"
	"sync"
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
	mu                  sync.RWMutex
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
	m.mu.RLock()
	defer m.mu.RUnlock()

	balance, exists := m.balances[playerID]
	if !exists {
		return 0, fmt.Errorf("player not found")
	}
	return balance, nil
}

// UpdatePlayerBalance updates a player's balance and records the transaction
func (m *InMemoryDB) UpdatePlayerBalance(playerID string, amount int64, transactionType, description string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

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
	m.mu.RLock()
	defer m.mu.RUnlock()

	transactions := m.transactions[playerID]
	if limit > 0 && limit < len(transactions) {
		return transactions[:limit], nil
	}
	return transactions, nil
}

// SaveTableState saves table state to memory
func (m *InMemoryDB) SaveTableState(tableState *db.TableState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tableStates[tableState.ID] = tableState
	return nil
}

// SaveSnapshot saves the table state and associated player states atomically (in-memory implementation).
func (m *InMemoryDB) SaveSnapshot(tableState *db.TableState, playerStates []*db.PlayerState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.tableStates[tableState.ID] = tableState

	// Clear previous player states for this table so the snapshot replaces them.
	m.playerStates[tableState.ID] = make(map[string]*db.PlayerState)
	for _, ps := range playerStates {
		m.playerStates[tableState.ID][ps.PlayerID] = ps
	}
	return nil
}

// LoadTableState loads table state from memory
func (m *InMemoryDB) LoadTableState(tableID string) (*db.TableState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, exists := m.tableStates[tableID]
	if !exists {
		return nil, fmt.Errorf("table state not found")
	}
	return state, nil
}

// DeleteTableState deletes table state from memory
func (m *InMemoryDB) DeleteTableState(tableID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tableStates, tableID)
	delete(m.playerStates, tableID)
	delete(m.disconnectedPlayers, tableID)
	return nil
}

// SavePlayerState saves player state to memory
func (m *InMemoryDB) SavePlayerState(tableID string, playerState *db.PlayerState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.playerStates[tableID] == nil {
		m.playerStates[tableID] = make(map[string]*db.PlayerState)
	}
	m.playerStates[tableID][playerState.PlayerID] = playerState
	return nil
}

// LoadPlayerStates loads all player states for a table from memory
func (m *InMemoryDB) LoadPlayerStates(tableID string) ([]*db.PlayerState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.playerStates[tableID] != nil {
		delete(m.playerStates[tableID], playerID)
	}
	if m.disconnectedPlayers[tableID] != nil {
		delete(m.disconnectedPlayers[tableID], playerID)
	}
	return nil
}

// GetAllTableIDs returns all table IDs
func (m *InMemoryDB) GetAllTableIDs() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
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

// TestSnapshotRestoresCurrentPlayer ensures that when a snapshot is taken while it is a particular
// player's turn (and that player subsequently disconnects), restoring the table from the persisted
// snapshot correctly identifies the same player as the current player to act.
func TestSnapshotRestoresCurrentPlayer(t *testing.T) {
	// Use the same in-memory DB for the two server instances so that persisted state survives.
	db := NewInMemoryDB()
	defer db.Close()

	logBackend := createTestLogBackend()
	defer logBackend.Close()

	// First server instance — runs the game and produces a snapshot.
	srv1 := &TestServer{Server: NewServer(db, logBackend)}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Players
	p1 := "p1"
	p2 := "p2"
	p3 := "p3"

	// Fund players
	for _, pid := range []string{p1, p2, p3} {
		_, err := srv1.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    pid,
			Amount:      5000,
			Description: "initial",
		})
		require.NoError(t, err)
	}

	// p1 creates table
	createResp, err := srv1.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:      p1,
		SmallBlind:    5,
		BigBlind:      10,
		MinPlayers:    3,
		MaxPlayers:    6,
		BuyIn:         100,
		StartingChips: 1000,
	})
	require.NoError(t, err)
	tableID := createResp.TableId

	// p2 and p3 join
	for _, pid := range []string{p2, p3} {
		joinResp, err := srv1.JoinTable(ctx, &pokerrpc.JoinTableRequest{
			PlayerId: pid,
			TableId:  tableID,
		})
		require.NoError(t, err)
		assert.True(t, joinResp.Success)
	}

	// Everyone ready
	for _, pid := range []string{p1, p2, p3} {
		_, err := srv1.SetPlayerReady(ctx, &pokerrpc.SetPlayerReadyRequest{
			PlayerId: pid,
			TableId:  tableID,
		})
		require.NoError(t, err)
	}

	// Wait for game to start
	var currentPlayer string
	for i := 0; i < 20; i++ {
		time.Sleep(25 * time.Millisecond)
		stateResp, err := srv1.GetGameState(ctx, &pokerrpc.GetGameStateRequest{TableId: tableID})
		require.NoError(t, err)

		if stateResp.GameState.GameStarted && stateResp.GameState.CurrentPlayer != "" {
			currentPlayer = stateResp.GameState.CurrentPlayer
			break
		}
	}
	require.NotEmpty(t, currentPlayer, "failed to retrieve current player")

	// Simulate the current player disconnecting.
	require.NoError(t, srv1.markPlayerDisconnected(tableID, currentPlayer))

	// Give the async persistence some time to complete safely.
	time.Sleep(50 * time.Millisecond)

	// Second server instance — loads the previously saved snapshot.
	srv2 := &TestServer{Server: NewServer(db, logBackend)}

	// After restoration, the same player should still be the current player to act.
	restoredState, err := srv2.GetGameState(ctx, &pokerrpc.GetGameStateRequest{TableId: tableID})
	require.NoError(t, err)
	assert.Equal(t, currentPlayer, restoredState.GameState.CurrentPlayer, "current player should be restored correctly from snapshot")
}

// Close properly stops the server and cleans up resources
func (ts *TestServer) Close() {
	if ts.Server != nil {
		ts.Server.Stop()
	}
}

// Add new test to verify correct blind posting and balances in heads-up game
func TestBlindPostingAndBalances(t *testing.T) {
	// Create isolated in-memory DB and server
	db := NewInMemoryDB()
	defer db.Close()

	logBackend := createTestLogBackend()
	defer logBackend.Close()

	srv := &TestServer{Server: NewServer(db, logBackend)}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Players
	p1 := "p1"
	p2 := "p2"

	// Fund players with sufficient DCR balance (atoms)
	for _, pid := range []string{p1, p2} {
		_, err := srv.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    pid,
			Amount:      5000,
			Description: "initial deposit",
		})
		require.NoError(t, err)
	}

	// p1 creates a heads-up table (minPlayers=2)
	const (
		startingChips int64 = 1000
		smallBlind          = int64(5)
		bigBlind            = int64(10)
	)

	createResp, err := srv.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:      p1,
		SmallBlind:    smallBlind,
		BigBlind:      bigBlind,
		MinPlayers:    2,
		MaxPlayers:    2,
		BuyIn:         100,
		StartingChips: startingChips,
	})
	require.NoError(t, err)
	tableID := createResp.TableId

	// p2 joins
	joinResp, err := srv.JoinTable(ctx, &pokerrpc.JoinTableRequest{PlayerId: p2, TableId: tableID})
	require.NoError(t, err)
	require.True(t, joinResp.Success)

	// Both players ready. Note: p1 ready first (common user flow)
	for _, pid := range []string{p1, p2} {
		_, err := srv.SetPlayerReady(ctx, &pokerrpc.SetPlayerReadyRequest{PlayerId: pid, TableId: tableID})
		require.NoError(t, err)
	}

	// Wait for game to start
	waitFor := func(cond func(*pokerrpc.GameUpdate) bool) *pokerrpc.GameUpdate {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			st, err := srv.GetGameState(ctx, &pokerrpc.GetGameStateRequest{TableId: tableID})
			require.NoError(t, err)
			if cond(st.GameState) {
				return st.GameState
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("condition not satisfied in time")
		return nil
	}

	gameState := waitFor(func(g *pokerrpc.GameUpdate) bool { return g.GameStarted && g.CurrentPlayer != "" })

	// Identify current player (should be small blind/dealer in heads-up)
	currentPlayer := gameState.CurrentPlayer
	require.Contains(t, []string{p1, p2}, currentPlayer)

	// Current player calls to match big blind
	_, err = srv.Call(ctx, &pokerrpc.CallRequest{PlayerId: currentPlayer, TableId: tableID})
	require.NoError(t, err)

	// Fetch updated state
	updatedState := waitFor(func(g *pokerrpc.GameUpdate) bool { return true })

	// Helper to fetch player info
	findPlayer := func(pid string) *pokerrpc.Player {
		for _, pl := range updatedState.Players {
			if pl.Id == pid {
				return pl
			}
		}
		return nil
	}

	p1Info := findPlayer(p1)
	p2Info := findPlayer(p2)
	require.NotNil(t, p1Info)
	require.NotNil(t, p2Info)

	// Both players should now have exactly bigBlind (10) committed and balances deducted once.
	expectedBalance := startingChips - bigBlind // 1000 - 10 = 990

	assert.Equal(t, bigBlind, p1Info.CurrentBet, "p1 CurrentBet should equal big blind once")
	assert.Equal(t, bigBlind, p2Info.CurrentBet, "p2 CurrentBet should equal big blind once")

	assert.Equal(t, expectedBalance, p1Info.Balance, "p1 balance incorrect after blinds and call")
	assert.Equal(t, expectedBalance, p2Info.Balance, "p2 balance incorrect after blinds and call")
}

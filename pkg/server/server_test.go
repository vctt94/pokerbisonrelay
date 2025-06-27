package server

import (
	"context"
	"os"
	"testing"
	"time"

	"net"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/poker-bisonrelay/pkg/server/internal/db"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// TestServer implements the PokerServiceServer interface
type TestServer struct {
	*Server
}

// InMemoryDB implements Database interface for testing
type InMemoryDB struct {
	balances     map[string]int64
	transactions map[string][]Transaction
}

// NewInMemoryDB creates a new in-memory database for testing
func NewInMemoryDB() *InMemoryDB {
	return &InMemoryDB{
		balances:     make(map[string]int64),
		transactions: make(map[string][]Transaction),
	}
}

// GetPlayerBalance returns the current balance of a player
func (db *InMemoryDB) GetPlayerBalance(playerID string) (int64, error) {
	return db.balances[playerID], nil
}

// UpdatePlayerBalance updates a player's balance and records the transaction
func (db *InMemoryDB) UpdatePlayerBalance(playerID string, amount int64, transactionType, description string) error {
	db.balances[playerID] += amount

	// Record transaction
	tx := Transaction{
		ID:          int64(len(db.transactions[playerID]) + 1),
		PlayerID:    playerID,
		Amount:      amount,
		Type:        transactionType,
		Description: description,
		CreatedAt:   time.Now().Format(time.RFC3339),
	}
	db.transactions[playerID] = append(db.transactions[playerID], tx)

	return nil
}

// GetPlayerTransactions returns the transaction history for a player
func (db *InMemoryDB) GetPlayerTransactions(playerID string, limit int) ([]Transaction, error) {
	transactions := db.transactions[playerID]
	if limit > 0 && limit < len(transactions) {
		return transactions[:limit], nil
	}
	return transactions, nil
}

// Close closes the database connection
func (db *InMemoryDB) Close() error {
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
	// Create a temporary database file
	dbPath := "test.db"
	defer os.Remove(dbPath)

	// Create a test database
	database, err := db.NewDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	// Create a test log backend
	logBackend := createTestLogBackend()
	defer logBackend.Close()

	// Create a new server
	server := &TestServer{
		Server: NewServer(database, logBackend),
	}

	// Register the server with gRPC
	s := grpc.NewServer()
	pokerrpc.RegisterPokerServiceServer(s, server)
	pokerrpc.RegisterLobbyServiceServer(s, server)

	// Test context
	ctx := context.Background()

	// Test player IDs
	player1ID := "player1"
	player2ID := "player2"

	// Test GetBalance
	t.Run("GetBalance", func(t *testing.T) {
		// Test non-existent player
		_, err := server.GetBalance(ctx, &pokerrpc.GetBalanceRequest{PlayerId: "non-existent"})
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
		assert.Contains(t, st.Message(), "player not found")

		// Create player first
		_, err = server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    player1ID,
			Amount:      0,
			Description: "initial balance",
		})
		require.NoError(t, err)

		// Test existing player
		resp, err := server.GetBalance(ctx, &pokerrpc.GetBalanceRequest{PlayerId: player1ID})
		require.NoError(t, err)
		assert.Equal(t, int64(0), resp.Balance)
	})

	// Test UpdateBalance
	t.Run("UpdateBalance", func(t *testing.T) {
		// Test deposit
		resp, err := server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    player1ID,
			Amount:      1000,
			Description: "initial deposit",
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1000), resp.NewBalance)

		// Test withdrawal
		resp, err = server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    player1ID,
			Amount:      -500,
			Description: "withdrawal",
		})
		require.NoError(t, err)
		assert.Equal(t, int64(500), resp.NewBalance)
	})

	// Test CreateTable
	t.Run("CreateTable", func(t *testing.T) {
		// Test with insufficient balance
		_, err := server.CreateTable(ctx, &pokerrpc.CreateTableRequest{
			PlayerId:      player1ID,
			SmallBlind:    10,
			BigBlind:      20,
			MinPlayers:    2,
			MaxPlayers:    6,
			BuyIn:         1000,
			StartingChips: 1000,
		})
		assert.Error(t, err)

		// Add more balance
		_, err = server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    player1ID,
			Amount:      2000,
			Description: "add more balance",
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

		// Add balance for player2 and join table (needed for subsequent tests)
		_, err = server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    player2ID,
			Amount:      1000,
			Description: "initial deposit",
		})
		require.NoError(t, err)

		// Player2 joins the table
		joinResp, err := server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
			PlayerId: player2ID,
			TableId:  tableID,
		})
		require.NoError(t, err)
		require.True(t, joinResp.Success)

		// Test GetGameState
		t.Run("GetGameState", func(t *testing.T) {
			// Test non-existent table
			_, err := server.GetGameState(ctx, &pokerrpc.GetGameStateRequest{
				TableId: "non-existent",
			})
			assert.Error(t, err)

			// Test existing table
			resp, err := server.GetGameState(ctx, &pokerrpc.GetGameStateRequest{
				TableId: tableID,
			})
			require.NoError(t, err)
			assert.Equal(t, tableID, resp.GameState.TableId)
			assert.Len(t, resp.GameState.Players, 2)
		})

		// Test MakeBet
		t.Run("MakeBet", func(t *testing.T) {
			// Test non-existent table
			_, err := server.MakeBet(ctx, &pokerrpc.MakeBetRequest{
				PlayerId: player1ID,
				TableId:  "non-existent",
				Amount:   20,
			})
			assert.Error(t, err)

			// Both players need to be ready before game can start
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

			// Wait a brief moment for game to start
			time.Sleep(50 * time.Millisecond)

			// Verify game has started
			gameState, err := server.GetGameState(ctx, &pokerrpc.GetGameStateRequest{
				TableId: tableID,
			})
			require.NoError(t, err)
			require.True(t, gameState.GameState.GameStarted, "game should have started after both players are ready")

			// Get the current player to act
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

		// Test LeaveTable
		t.Run("LeaveTable", func(t *testing.T) {
			// Test non-existent table
			resp, err := server.LeaveTable(ctx, &pokerrpc.LeaveTableRequest{
				PlayerId: player1ID,
				TableId:  "non-existent",
			})
			require.NoError(t, err)
			assert.False(t, resp.Success)

			// Test successful leave
			resp, err = server.LeaveTable(ctx, &pokerrpc.LeaveTableRequest{
				PlayerId: player1ID,
				TableId:  tableID,
			})
			require.NoError(t, err)
			assert.True(t, resp.Success)
		})

		// Test JoinTable
		t.Run("JoinTable", func(t *testing.T) {
			// Test joining non-existent table
			resp, err := server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
				PlayerId: player2ID,
				TableId:  "non-existent",
			})
			require.NoError(t, err)
			assert.False(t, resp.Success)

			// Test joining when already at table
			resp, err = server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
				PlayerId: player2ID,
				TableId:  tableID,
			})
			require.NoError(t, err)
			assert.False(t, resp.Success) // Should fail since player2 is already at table
		})
	})
}

func TestPokerGameFlow(t *testing.T) {
	// Create a new server
	db := NewInMemoryDB()
	logBackend := createTestLogBackend()
	defer logBackend.Close()
	server := NewServer(db, logBackend)

	// Start gRPC server
	lis, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer()
	pokerrpc.RegisterPokerServiceServer(s, server)
	pokerrpc.RegisterLobbyServiceServer(s, server)
	go func() {
		if err := s.Serve(lis); err != nil {
			t.Errorf("failed to serve: %v", err)
		}
	}()
	defer s.Stop()

	// Create gRPC client connections
	conn, err := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// Create clients
	lobbyClient := pokerrpc.NewLobbyServiceClient(conn)
	pokerClient := pokerrpc.NewPokerServiceClient(conn)

	// Create two players
	player1ID := "player1"
	player2ID := "player2"
	initialBalance := int64(1000)

	// Set initial balances
	_, err = lobbyClient.UpdateBalance(context.Background(), &pokerrpc.UpdateBalanceRequest{
		PlayerId: player1ID,
		Amount:   initialBalance,
	})
	if err != nil {
		t.Fatalf("failed to set player1 balance: %v", err)
	}

	_, err = lobbyClient.UpdateBalance(context.Background(), &pokerrpc.UpdateBalanceRequest{
		PlayerId: player2ID,
		Amount:   initialBalance,
	})
	if err != nil {
		t.Fatalf("failed to set player2 balance: %v", err)
	}

	// Player1 creates a table
	createTableResp, err := lobbyClient.CreateTable(context.Background(), &pokerrpc.CreateTableRequest{
		PlayerId:      player1ID,
		BuyIn:         100,
		MinPlayers:    2,
		MaxPlayers:    2,
		SmallBlind:    5,
		BigBlind:      10,
		MinBalance:    100,
		StartingChips: 500,
	})
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}
	tableID := createTableResp.TableId

	// Player2 joins the table
	_, err = lobbyClient.JoinTable(context.Background(), &pokerrpc.JoinTableRequest{
		PlayerId: player2ID,
		TableId:  tableID,
	})
	if err != nil {
		t.Fatalf("failed to join table: %v", err)
	}

	// Both players set ready
	_, err = lobbyClient.SetPlayerReady(context.Background(), &pokerrpc.SetPlayerReadyRequest{
		PlayerId: player1ID,
		TableId:  tableID,
	})
	if err != nil {
		t.Fatalf("failed to set player1 ready: %v", err)
	}

	_, err = lobbyClient.SetPlayerReady(context.Background(), &pokerrpc.SetPlayerReadyRequest{
		PlayerId: player2ID,
		TableId:  tableID,
	})
	if err != nil {
		t.Fatalf("failed to set player2 ready: %v", err)
	}

	// Start game stream for both players
	stream1, err := pokerClient.StartGameStream(context.Background(), &pokerrpc.StartGameStreamRequest{
		PlayerId: player1ID,
		TableId:  tableID,
	})
	if err != nil {
		t.Fatalf("failed to start game stream for player1: %v", err)
	}
	defer stream1.CloseSend()

	stream2, err := pokerClient.StartGameStream(context.Background(), &pokerrpc.StartGameStreamRequest{
		PlayerId: player2ID,
		TableId:  tableID,
	})
	if err != nil {
		t.Fatalf("failed to start game stream for player2: %v", err)
	}
	defer stream2.CloseSend()

	// Wait for game to start
	time.Sleep(100 * time.Millisecond)

	// Get initial game state
	state, err := pokerClient.GetGameState(context.Background(), &pokerrpc.GetGameStateRequest{
		TableId: tableID,
	})
	if err != nil {
		t.Fatalf("failed to get game state: %v", err)
	}

	if !state.GameState.GameStarted {
		t.Error("game should have started")
	}

	// Player1 makes a bet
	_, err = pokerClient.MakeBet(context.Background(), &pokerrpc.MakeBetRequest{
		PlayerId: player1ID,
		TableId:  tableID,
		Amount:   20,
	})
	if err != nil {
		t.Fatalf("failed to make bet: %v", err)
	}

	// Player2 calls
	_, err = pokerClient.MakeBet(context.Background(), &pokerrpc.MakeBetRequest{
		PlayerId: player2ID,
		TableId:  tableID,
		Amount:   20,
	})
	if err != nil {
		t.Fatalf("failed to make bet: %v", err)
	}

	// Get final game state
	state, err = pokerClient.GetGameState(context.Background(), &pokerrpc.GetGameStateRequest{
		TableId: tableID,
	})
	if err != nil {
		t.Fatalf("failed to get final game state: %v", err)
	}

	// Verify pot size
	if state.GameState.Pot != 40 {
		t.Errorf("expected pot size 40, got %d", state.GameState.Pot)
	}
}

func TestHostLeavesTableTransfersHost(t *testing.T) {
	// Create a new server
	db := NewInMemoryDB()
	logBackend := createTestLogBackend()
	defer logBackend.Close()
	server := NewServer(db, logBackend)
	ctx := context.Background()

	// Create three players
	hostID := "host"
	player1ID := "player1"
	player2ID := "player2"
	initialBalance := int64(1000)

	// Set initial balances
	for _, playerID := range []string{hostID, player1ID, player2ID} {
		_, err := server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId: playerID,
			Amount:   initialBalance,
		})
		require.NoError(t, err)
	}

	// Host creates a table
	createTableResp, err := server.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:      hostID,
		BuyIn:         100,
		MinPlayers:    2,
		MaxPlayers:    3,
		SmallBlind:    5,
		BigBlind:      10,
		MinBalance:    100,
		StartingChips: 500,
	})
	require.NoError(t, err)
	tableID := createTableResp.TableId

	// Player1 and Player2 join the table
	_, err = server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
		PlayerId: player1ID,
		TableId:  tableID,
	})
	require.NoError(t, err)

	_, err = server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
		PlayerId: player2ID,
		TableId:  tableID,
	})
	require.NoError(t, err)

	// Verify table exists and has 3 players
	tablesResp, err := server.GetTables(ctx, &pokerrpc.GetTablesRequest{})
	require.NoError(t, err)
	require.Len(t, tablesResp.Tables, 1)
	assert.Equal(t, int32(3), tablesResp.Tables[0].CurrentPlayers)
	assert.Equal(t, hostID, tablesResp.Tables[0].HostId, "Original host should be correct")

	// Host leaves the table
	leaveResp, err := server.LeaveTable(ctx, &pokerrpc.LeaveTableRequest{
		PlayerId: hostID,
		TableId:  tableID,
	})
	require.NoError(t, err)
	assert.True(t, leaveResp.Success)
	assert.Contains(t, leaveResp.Message, "Host transferred to", "Host should be transferred")

	// Verify table still exists with 2 players and new host
	tablesResp, err = server.GetTables(ctx, &pokerrpc.GetTablesRequest{})
	require.NoError(t, err)
	assert.Len(t, tablesResp.Tables, 1, "Table should still exist when host leaves but other players remain")
	assert.Equal(t, int32(2), tablesResp.Tables[0].CurrentPlayers, "Table should have 2 players remaining")

	// Verify host has been transferred to one of the remaining players
	newHostID := tablesResp.Tables[0].HostId
	assert.NotEqual(t, hostID, newHostID, "Host should be different from original host")
	assert.True(t, newHostID == player1ID || newHostID == player2ID, "New host should be one of the remaining players")
}

func TestLastPlayerLeavesTableClosure(t *testing.T) {
	// Create a new server
	db := NewInMemoryDB()
	logBackend := createTestLogBackend()
	defer logBackend.Close()
	server := NewServer(db, logBackend)
	ctx := context.Background()

	// Create one player
	hostID := "host"
	initialBalance := int64(1000)

	// Set initial balance
	_, err := server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
		PlayerId: hostID,
		Amount:   initialBalance,
	})
	require.NoError(t, err)

	// Host creates a table
	createTableResp, err := server.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:      hostID,
		BuyIn:         100,
		MinPlayers:    2,
		MaxPlayers:    3,
		SmallBlind:    5,
		BigBlind:      10,
		MinBalance:    100,
		StartingChips: 500,
	})
	require.NoError(t, err)
	tableID := createTableResp.TableId

	// Verify table exists and has 1 player
	tablesResp, err := server.GetTables(ctx, &pokerrpc.GetTablesRequest{})
	require.NoError(t, err)
	require.Len(t, tablesResp.Tables, 1)
	assert.Equal(t, int32(1), tablesResp.Tables[0].CurrentPlayers)

	// Host leaves the table (only player)
	leaveResp, err := server.LeaveTable(ctx, &pokerrpc.LeaveTableRequest{
		PlayerId: hostID,
		TableId:  tableID,
	})
	require.NoError(t, err)
	assert.True(t, leaveResp.Success)
	assert.Equal(t, "Host left - table closed (no other players)", leaveResp.Message)

	// Verify table is removed from server
	tablesResp, err = server.GetTables(ctx, &pokerrpc.GetTablesRequest{})
	require.NoError(t, err)
	assert.Len(t, tablesResp.Tables, 0, "Table should be removed when last player leaves")
}

func TestNonHostLeavesTable(t *testing.T) {
	// Create a new server
	db := NewInMemoryDB()
	logBackend := createTestLogBackend()
	defer logBackend.Close()
	server := NewServer(db, logBackend)
	ctx := context.Background()

	// Create two players
	hostID := "host"
	playerID := "player"
	initialBalance := int64(1000)

	// Set initial balances
	for _, id := range []string{hostID, playerID} {
		_, err := server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId: id,
			Amount:   initialBalance,
		})
		require.NoError(t, err)
	}

	// Host creates a table
	createTableResp, err := server.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:      hostID,
		BuyIn:         100,
		MinPlayers:    2,
		MaxPlayers:    2,
		SmallBlind:    5,
		BigBlind:      10,
		MinBalance:    100,
		StartingChips: 500,
	})
	require.NoError(t, err)
	tableID := createTableResp.TableId

	// Player1 and Player2 join the table
	_, err = server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
		PlayerId: playerID,
		TableId:  tableID,
	})
	require.NoError(t, err)

	// Verify table exists and has 2 players
	tablesResp, err := server.GetTables(ctx, &pokerrpc.GetTablesRequest{})
	require.NoError(t, err)
	require.Len(t, tablesResp.Tables, 1)
	assert.Equal(t, int32(2), tablesResp.Tables[0].CurrentPlayers)

	// Non-host player leaves the table
	leaveResp, err := server.LeaveTable(ctx, &pokerrpc.LeaveTableRequest{
		PlayerId: playerID,
		TableId:  tableID,
	})
	require.NoError(t, err)
	assert.True(t, leaveResp.Success)
	assert.Equal(t, "Successfully left table", leaveResp.Message)

	// Verify table still exists (should not be closed when non-host leaves)
	tablesResp, err = server.GetTables(ctx, &pokerrpc.GetTablesRequest{})
	require.NoError(t, err)
	assert.Len(t, tablesResp.Tables, 1, "Table should still exist when non-host leaves")
	assert.Equal(t, int32(1), tablesResp.Tables[0].CurrentPlayers, "Table should have 1 player remaining")
}

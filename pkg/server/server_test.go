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

func TestPokerService(t *testing.T) {
	// Create a temporary database file
	dbPath := "test.db"
	defer os.Remove(dbPath)

	// Create a test database
	database, err := db.NewDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	// Create a new server
	server := &TestServer{
		Server: NewServer(database),
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
			PlayerId:   player1ID,
			SmallBlind: 10,
			BigBlind:   20,
			MinPlayers: 2,
			MaxPlayers: 6,
			BuyIn:      1000,
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
			PlayerId:   player1ID,
			SmallBlind: 10,
			BigBlind:   20,
			MinPlayers: 2,
			MaxPlayers: 6,
			BuyIn:      1000,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, resp.TableId)
		tableID := resp.TableId

		// Test JoinTable
		t.Run("JoinTable", func(t *testing.T) {
			// Test joining non-existent table
			resp, err := server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
				PlayerId: player2ID,
				TableId:  "non-existent",
			})
			require.NoError(t, err)
			assert.False(t, resp.Success)

			// Add balance for player2
			_, err = server.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
				PlayerId:    player2ID,
				Amount:      1000,
				Description: "initial deposit",
			})
			require.NoError(t, err)

			// Test successful join
			resp, err = server.JoinTable(ctx, &pokerrpc.JoinTableRequest{
				PlayerId: player2ID,
				TableId:  tableID,
			})
			require.NoError(t, err)
			assert.True(t, resp.Success)
		})

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

			// Test successful bet
			resp, err := server.MakeBet(ctx, &pokerrpc.MakeBetRequest{
				PlayerId: player1ID,
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
	})
}

func TestPokerGameFlow(t *testing.T) {
	// Create a new server
	db := NewInMemoryDB()
	server := NewServer(db)

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
		PlayerId:   player1ID,
		BuyIn:      100,
		MinPlayers: 2,
		MaxPlayers: 2,
		SmallBlind: 5,
		BigBlind:   10,
		MinBalance: 100,
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

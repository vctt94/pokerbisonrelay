// This file contains end-to-end tests that spin up a full poker server backed
// by a real SQLite database. The tests exercise realistic gameplay flows with
// minimal mocking (only the network is in-process via gRPC).
//
// To keep the tests self-contained and independent they **must** be executed
// with `go test ./...` and **should not** depend on external resources.

package e2e

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"net"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/poker-bisonrelay/pkg/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// testEnv holds the runtime components that make up a fully
// functional instance of the poker server backed by a *real* SQLite
// database. Each E2E test spins-up its own env so tests are completely
// isolated and can run in parallel.
type testEnv struct {
	t           *testing.T
	db          server.Database
	grpcSrv     *grpc.Server
	conn        *grpc.ClientConn
	lobbyClient pokerrpc.LobbyServiceClient
	pokerClient pokerrpc.PokerServiceClient
}

// newTestEnv creates, starts and returns a ready-to-use environment.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	// 1. NEW TEMPORARY DATABASE -------------------------------------------------
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "poker.sqlite")
	database, err := server.NewDatabase(dbPath)
	require.NoError(t, err)

	// 2. GRPC SERVER ------------------------------------------------------------
	pokerSrv := server.NewServer(database)
	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)

	grpcSrv := grpc.NewServer()
	pokerrpc.RegisterLobbyServiceServer(grpcSrv, pokerSrv)
	pokerrpc.RegisterPokerServiceServer(grpcSrv, pokerSrv)
	go func() { _ = grpcSrv.Serve(lis) }()

	// 3. GRPC CLIENT CONNECTION --------------------------------------------------
	conn, err := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	return &testEnv{
		t:           t,
		db:          database,
		grpcSrv:     grpcSrv,
		conn:        conn,
		lobbyClient: pokerrpc.NewLobbyServiceClient(conn),
		pokerClient: pokerrpc.NewPokerServiceClient(conn),
	}
}

// Close gracefully shuts down all resources.
func (e *testEnv) Close() {
	e.conn.Close()
	e.grpcSrv.Stop()
	_ = e.db.Close()
}

// setBalance is a small helper that ensures the player has exactly the
// specified balance by calculating the delta against the current stored
// balance and issuing a single UpdateBalance call.
func (e *testEnv) setBalance(ctx context.Context, playerID string, balance int64) {
	var currBal int64
	if resp, err := e.lobbyClient.GetBalance(ctx, &pokerrpc.GetBalanceRequest{PlayerId: playerID}); err == nil {
		currBal = resp.GetBalance()
	}
	delta := balance - currBal
	if delta == 0 {
		return
	}
	_, err := e.lobbyClient.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
		PlayerId:    playerID,
		Amount:      delta,
		Description: "seed balance",
	})
	require.NoError(e.t, err)
}

// waitForGameStart polls GetGameState until GameStarted==true or the timeout
// expires (in which case the test fails).
func (e *testEnv) waitForGameStart(ctx context.Context, tableID string, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		resp, err := e.pokerClient.GetGameState(ctx, &pokerrpc.GetGameStateRequest{TableId: tableID})
		if err == nil && resp.GameState.GetGameStarted() {
			return
		}
		select {
		case <-ctx.Done():
			e.t.Fatalf("game did not start within %s", timeout)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// waitForGamePhase polls GetGameState until the given phase is reached or the timeout expires
func (e *testEnv) waitForGamePhase(ctx context.Context, tableID string, phase pokerrpc.GamePhase, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		resp, err := e.pokerClient.GetGameState(ctx, &pokerrpc.GetGameStateRequest{TableId: tableID})
		if err == nil && resp.GameState.GetPhase() == phase {
			return
		}
		select {
		case <-ctx.Done():
			e.t.Fatalf("game did not reach phase %s within %s", phase, timeout)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// getBalance is syntactic sugar to fetch a player's current balance.
func (e *testEnv) getBalance(ctx context.Context, playerID string) int64 {
	resp, err := e.lobbyClient.GetBalance(ctx, &pokerrpc.GetBalanceRequest{PlayerId: playerID})
	require.NoError(e.t, err)
	return resp.Balance
}

// getGameState is a helper to get the current game state
func (e *testEnv) getGameState(ctx context.Context, tableID string) *pokerrpc.GameUpdate {
	resp, err := e.pokerClient.GetGameState(ctx, &pokerrpc.GetGameStateRequest{TableId: tableID})
	require.NoError(e.t, err)
	return resp.GameState
}

// createStandardTable creates a table with standard settings for testing
func (e *testEnv) createStandardTable(ctx context.Context, creatorID string, minPlayers, maxPlayers int) string {
	createResp, err := e.lobbyClient.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:   creatorID,
		SmallBlind: 10,
		BigBlind:   20,
		MinPlayers: int32(minPlayers),
		MaxPlayers: int32(maxPlayers),
		BuyIn:      1_000,
		MinBalance: 1_000,
	})
	require.NoError(e.t, err)
	assert.NotEmpty(e.t, createResp.TableId)
	return createResp.TableId
}

// -----------------------------------------------------------------------------
//
//	SCENARIO: Full Sit'n'Go with 3 players
//
// -----------------------------------------------------------------------------
func TestSitAndGoEndToEnd(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	defer env.Close()

	ctx := context.Background()

	players := []string{"alice", "bob", "carol"}
	initialBankroll := int64(10_000) // satoshi-style units (1e-8 DCR)
	for _, p := range players {
		env.setBalance(ctx, p, initialBankroll)
	}

	// Alice creates a new table that acts like a Sit'n'Go (auto-start when all
	// players are ready).
	createResp, err := env.lobbyClient.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:   "alice",
		SmallBlind: 10,
		BigBlind:   20,
		MinPlayers: 3,
		MaxPlayers: 3,
		BuyIn:      1_000,
		MinBalance: 1_000,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, createResp.TableId)
	tableID := createResp.TableId

	// Bob & Carol join the table.
	for _, p := range []string{"bob", "carol"} {
		_, err := env.lobbyClient.JoinTable(ctx, &pokerrpc.JoinTableRequest{
			PlayerId: p,
			TableId:  tableID,
		})
		require.NoError(t, err)
	}

	// Everyone marks themselves as ready.
	for _, p := range players {
		_, err := env.lobbyClient.SetPlayerReady(ctx, &pokerrpc.SetPlayerReadyRequest{
			PlayerId: p,
			TableId:  tableID,
		})
		require.NoError(t, err)
	}

	// Verify that all players are marked as ready
	gameState := env.getGameState(ctx, tableID)
	assert.True(t, gameState.GetPlayersJoined() == 3, "expected 3 players joined")

	// Wait until the server flags the game as started.
	env.waitForGameStart(ctx, tableID, 3*time.Second)

	// Quick sanity check of balances after table creation/join.
	//  - Table creator (alice) keeps her bankroll untouched (implementation detail)
	//  - Joiners (bob & carol) have bankroll - buyIn.
	buyIn := int64(1_000)
	assert.Equal(t, initialBankroll, env.getBalance(ctx, "alice"))
	for _, p := range []string{"bob", "carol"} {
		assert.Equal(t, initialBankroll-buyIn, env.getBalance(ctx, p), "post buy-in balance mismatch for %s", p)
	}

	// ACTION ROUND -------------------------------------------------------------
	// Alice opens with a 100 bet.
	_, err = env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "alice",
		TableId:  tableID,
		Amount:   100,
	})
	require.NoError(t, err)

	// Bob calls.
	_, err = env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "bob",
		TableId:  tableID,
		Amount:   100,
	})
	require.NoError(t, err)

	// Carol decides to fold (0 bet via Fold API).
	_, err = env.pokerClient.Fold(ctx, &pokerrpc.FoldRequest{
		PlayerId: "carol",
		TableId:  tableID,
	})
	require.NoError(t, err)

	// Validate pot value (200) via GetGameState.
	state, err := env.pokerClient.GetGameState(ctx, &pokerrpc.GetGameStateRequest{TableId: tableID})
	require.NoError(t, err)
	assert.Equal(t, int64(200), state.GameState.Pot, "unexpected pot size")

	// Verify Carol is marked as folded
	for _, player := range state.GameState.Players {
		if player.Id == "carol" {
			assert.True(t, player.Folded, "carol should be marked as folded")
		}
	}

	// Verify the remaining active players
	activePlayers := 0
	for _, player := range state.GameState.Players {
		if !player.Folded {
			activePlayers++
		}
	}
	assert.Equal(t, 2, activePlayers, "expected 2 active players")

	// FINISHING ACTIONS --------------------------------------------------------
	// Alice tips Carol 150 for good sportsmanship using the real tip handler.
	_, err = env.lobbyClient.ProcessTip(ctx, &pokerrpc.ProcessTipRequest{
		FromPlayerId: "alice",
		ToPlayerId:   "carol",
		Amount:       150,
		Message:      "good fold",
	})
	require.NoError(t, err)

	// Verify balances post-tip.
	aliceBal := env.getBalance(ctx, "alice")
	carolBal := env.getBalance(ctx, "carol")
	assert.Equal(t, initialBankroll-150, aliceBal)
	assert.Equal(t, initialBankroll-buyIn+150, carolBal)
}

// -----------------------------------------------------------------------------
//
//	SCENARIO: Complete Hand Flow - All Betting Rounds with 4 players
//
// -----------------------------------------------------------------------------
func TestCompleteHandFlow(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	defer env.Close()

	ctx := context.Background()

	// Setup players with initial bankrolls
	players := []string{"player1", "player2", "player3", "player4"}
	initialBankroll := int64(10_000)
	for _, p := range players {
		env.setBalance(ctx, p, initialBankroll)
	}

	// Player1 creates a table for 4 players
	tableID := env.createStandardTable(ctx, "player1", 4, 4)

	// All players join the table
	for _, p := range players[1:] { // Skip player1 who already created the table
		_, err := env.lobbyClient.JoinTable(ctx, &pokerrpc.JoinTableRequest{
			PlayerId: p,
			TableId:  tableID,
		})
		require.NoError(t, err)
	}

	// All players mark themselves as ready
	for _, p := range players {
		_, err := env.lobbyClient.SetPlayerReady(ctx, &pokerrpc.SetPlayerReadyRequest{
			PlayerId: p,
			TableId:  tableID,
		})
		require.NoError(t, err)
	}

	// Wait for game to start
	env.waitForGameStart(ctx, tableID, 3*time.Second)

	// PRE-FLOP BETTING
	// Player1 calls the big blind
	_, err := env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "player1",
		TableId:  tableID,
		Amount:   20,
	})
	require.NoError(t, err)

	// Player2 raises
	_, err = env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "player2",
		TableId:  tableID,
		Amount:   60, // Raising to 60
	})
	require.NoError(t, err)

	// Player3 calls the raise
	_, err = env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "player3",
		TableId:  tableID,
		Amount:   60,
	})
	require.NoError(t, err)

	// Player4 folds
	_, err = env.pokerClient.Fold(ctx, &pokerrpc.FoldRequest{
		PlayerId: "player4",
		TableId:  tableID,
	})
	require.NoError(t, err)

	// Player1 calls the raise
	_, err = env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "player1",
		TableId:  tableID,
		Amount:   60,
	})
	require.NoError(t, err)

	// Check pot after pre-flop
	state := env.getGameState(ctx, tableID)
	assert.Equal(t, int64(180), state.Pot, "unexpected pot size after pre-flop")

	// FLOP ROUND
	// Wait for flop to be dealt
	env.waitForGamePhase(ctx, tableID, pokerrpc.GamePhase_FLOP, 3*time.Second)

	// Make sure we have 3 community cards
	state = env.getGameState(ctx, tableID)
	assert.Equal(t, 3, len(state.CommunityCards), "expected 3 community cards after flop")

	// Player1 checks
	_, err = env.pokerClient.Check(ctx, &pokerrpc.CheckRequest{
		PlayerId: "player1",
		TableId:  tableID,
	})
	require.NoError(t, err)

	// Player2 bets 100
	_, err = env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "player2",
		TableId:  tableID,
		Amount:   100,
	})
	require.NoError(t, err)

	// Player3 calls
	_, err = env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "player3",
		TableId:  tableID,
		Amount:   100,
	})
	require.NoError(t, err)

	// Player1 folds
	_, err = env.pokerClient.Fold(ctx, &pokerrpc.FoldRequest{
		PlayerId: "player1",
		TableId:  tableID,
	})
	require.NoError(t, err)

	// Check pot after flop
	state = env.getGameState(ctx, tableID)
	assert.Equal(t, int64(380), state.Pot, "unexpected pot size after flop")

	// TURN ROUND
	// Wait for turn card
	env.waitForGamePhase(ctx, tableID, pokerrpc.GamePhase_TURN, 3*time.Second)

	// Make sure we have 4 community cards
	state = env.getGameState(ctx, tableID)
	assert.Equal(t, 4, len(state.CommunityCards), "expected 4 community cards after turn")

	// Player2 bets 200
	_, err = env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "player2",
		TableId:  tableID,
		Amount:   200,
	})
	require.NoError(t, err)

	// Player3 calls
	_, err = env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "player3",
		TableId:  tableID,
		Amount:   200,
	})
	require.NoError(t, err)

	// Check pot after turn
	state = env.getGameState(ctx, tableID)
	assert.Equal(t, int64(780), state.Pot, "unexpected pot size after turn")

	// RIVER ROUND
	// Wait for river card
	env.waitForGamePhase(ctx, tableID, pokerrpc.GamePhase_RIVER, 3*time.Second)

	// Make sure we have 5 community cards
	state = env.getGameState(ctx, tableID)
	assert.Equal(t, 5, len(state.CommunityCards), "expected 5 community cards after river")

	// Player2 checks
	_, err = env.pokerClient.Check(ctx, &pokerrpc.CheckRequest{
		PlayerId: "player2",
		TableId:  tableID,
	})
	require.NoError(t, err)

	// Player3 bets 300
	_, err = env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "player3",
		TableId:  tableID,
		Amount:   300,
	})
	require.NoError(t, err)

	// Player2 folds
	_, err = env.pokerClient.Fold(ctx, &pokerrpc.FoldRequest{
		PlayerId: "player2",
		TableId:  tableID,
	})
	require.NoError(t, err)

	// Wait for showdown
	env.waitForGamePhase(ctx, tableID, pokerrpc.GamePhase_SHOWDOWN, 3*time.Second)

	// Verify Player3 won the pot
	winners, err := env.pokerClient.GetWinners(ctx, &pokerrpc.GetWinnersRequest{
		TableId: tableID,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, len(winners.Winners), "expected 1 winner")
	assert.Equal(t, "player3", winners.Winners[0].PlayerId, "expected player3 to win")

	// Verify pot amount in winner response
	assert.Equal(t, int64(780), winners.Pot, "unexpected pot amount in winner response")
}

// -----------------------------------------------------------------------------
//
//	SCENARIO: Test Player Timeout and Auto-Fold
//
// -----------------------------------------------------------------------------
func TestPlayerTimeoutAutoFold(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	defer env.Close()

	ctx := context.Background()

	// Setup players
	players := []string{"active1", "active2", "timeout"}
	initialBankroll := int64(10_000)
	for _, p := range players {
		env.setBalance(ctx, p, initialBankroll)
	}

	// Create table with short timebank
	createResp, err := env.lobbyClient.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:   "active1",
		SmallBlind: 10,
		BigBlind:   20,
		MinPlayers: 3,
		MaxPlayers: 3,
		BuyIn:      1_000,
		MinBalance: 1_000,
	})
	require.NoError(t, err)
	tableID := createResp.TableId

	// All players join and mark ready
	for _, p := range players[1:] {
		_, err := env.lobbyClient.JoinTable(ctx, &pokerrpc.JoinTableRequest{
			PlayerId: p,
			TableId:  tableID,
		})
		require.NoError(t, err)
	}

	for _, p := range players {
		_, err := env.lobbyClient.SetPlayerReady(ctx, &pokerrpc.SetPlayerReadyRequest{
			PlayerId: p,
			TableId:  tableID,
		})
		require.NoError(t, err)
	}

	// Wait for game to start
	env.waitForGameStart(ctx, tableID, 3*time.Second)

	// active1 and active2 make their moves
	_, err = env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "active1",
		TableId:  tableID,
		Amount:   20,
	})
	require.NoError(t, err)

	_, err = env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "active2",
		TableId:  tableID,
		Amount:   20,
	})
	require.NoError(t, err)

	// But "timeout" player doesn't act - should auto-fold after timeout
	// Wait for enough time for auto-fold to occur (timebank + buffer)
	time.Sleep(7 * time.Second)

	// Check if player was auto-folded
	state := env.getGameState(ctx, tableID)
	for _, player := range state.Players {
		if player.Id == "timeout" {
			assert.True(t, player.Folded, "timeout player should have been auto-folded")
		}
	}
}

// -----------------------------------------------------------------------------
//
//	SCENARIO: Basic table creation and player readiness
//
// -----------------------------------------------------------------------------
func TestBasicTableAndReadiness(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	defer env.Close()

	ctx := context.Background()

	// Setup players with initial bankrolls
	players := []string{"player1", "player2", "player3", "player4"}
	initialBankroll := int64(10_000)
	for _, p := range players {
		env.setBalance(ctx, p, initialBankroll)
	}

	// Player1 creates a table for 4 players
	tableID := env.createStandardTable(ctx, "player1", 4, 4)

	// All players join the table
	for _, p := range players[1:] { // Skip player1 who already created the table
		_, err := env.lobbyClient.JoinTable(ctx, &pokerrpc.JoinTableRequest{
			PlayerId: p,
			TableId:  tableID,
		})
		require.NoError(t, err)
	}

	// Verify initial state
	state := env.getGameState(ctx, tableID)
	assert.Equal(t, int32(4), state.PlayersJoined, "expected 4 players joined")
	assert.Equal(t, int32(4), state.PlayersRequired, "expected 4 players required")
	assert.False(t, state.GameStarted, "game should not be started yet")

	// Set players ready one by one and check state
	for i, p := range players {
		_, err := env.lobbyClient.SetPlayerReady(ctx, &pokerrpc.SetPlayerReadyRequest{
			PlayerId: p,
			TableId:  tableID,
		})
		require.NoError(t, err)

		// Check that player is marked as ready
		state = env.getGameState(ctx, tableID)
		readyCount := 0
		for _, player := range state.Players {
			if player.IsReady {
				readyCount++
			}
		}
		assert.Equal(t, i+1, readyCount, "expected %d players ready", i+1)
	}

	// Now all players are ready, game should start
	env.waitForGameStart(ctx, tableID, 3*time.Second)
}

// -----------------------------------------------------------------------------
//
//	SCENARIO: Test basic betting
//
// -----------------------------------------------------------------------------
func TestBasicBetting(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	defer env.Close()

	ctx := context.Background()

	// Setup 3 players
	players := []string{"p1", "p2", "p3"}
	initialBankroll := int64(10_000)
	for _, p := range players {
		env.setBalance(ctx, p, initialBankroll)
	}

	// Create and join table
	tableID := env.createStandardTable(ctx, "p1", 3, 3)
	for _, p := range players[1:] {
		_, err := env.lobbyClient.JoinTable(ctx, &pokerrpc.JoinTableRequest{
			PlayerId: p,
			TableId:  tableID,
		})
		require.NoError(t, err)
	}

	// Set all players ready
	for _, p := range players {
		_, err := env.lobbyClient.SetPlayerReady(ctx, &pokerrpc.SetPlayerReadyRequest{
			PlayerId: p,
			TableId:  tableID,
		})
		require.NoError(t, err)
	}

	// Wait for game to start
	env.waitForGameStart(ctx, tableID, 3*time.Second)

	// Verify initial pot is 0
	state := env.getGameState(ctx, tableID)
	assert.Equal(t, int64(0), state.Pot, "initial pot should be 0")

	// P1 bets 50
	_, err := env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "p1",
		TableId:  tableID,
		Amount:   50,
	})
	require.NoError(t, err)

	// Check pot is now 50
	state = env.getGameState(ctx, tableID)
	assert.Equal(t, int64(50), state.Pot, "pot should be 50 after p1's bet")

	// P2 calls
	_, err = env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "p2",
		TableId:  tableID,
		Amount:   50,
	})
	require.NoError(t, err)

	// Check pot is now 100
	state = env.getGameState(ctx, tableID)
	assert.Equal(t, int64(100), state.Pot, "pot should be 100 after p2's call")

	// P3 raises to 150
	_, err = env.pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: "p3",
		TableId:  tableID,
		Amount:   150,
	})
	require.NoError(t, err)

	// Check pot is now 250
	state = env.getGameState(ctx, tableID)
	assert.Equal(t, int64(250), state.Pot, "pot should be 250 after p3's raise")
}

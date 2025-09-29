package server

import (
	"context"
	"testing"

	"github.com/vctt94/pokerbisonrelay/pkg/poker"
	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
	"google.golang.org/grpc/metadata"
)

// ---------- Stub implementations used across unit tests ---------- //

// mockNotificationStream is a lightweight implementation of the
// LobbyService_StartNotificationStreamServer interface that records the
// notifications sent by server.notifyPlayers.
// It implements only the methods actually used by the code-under-test.

type mockNotificationStream struct {
	sent []*pokerrpc.Notification
}

// Ensure the mock satisfies the required interface at compile-time.
var _ pokerrpc.LobbyService_StartNotificationStreamServer = (*mockNotificationStream)(nil)

// Send records the notification for inspection.
func (m *mockNotificationStream) Send(n *pokerrpc.Notification) error {
	m.sent = append(m.sent, n)
	return nil
}

// ----- grpc.ServerStream interface stubs ----- //

func (m *mockNotificationStream) SetHeader(metadata.MD) error  { return nil }
func (m *mockNotificationStream) SendHeader(metadata.MD) error { return nil }
func (m *mockNotificationStream) SetTrailer(metadata.MD)       {}
func (m *mockNotificationStream) Context() context.Context     { return context.TODO() }
func (m *mockNotificationStream) SendMsg(interface{}) error    { return nil }
func (m *mockNotificationStream) RecvMsg(interface{}) error    { return nil }

// TestGameStateHandlerBuildGameStates verifies that game updates are correctly
// built from a table snapshot and that hole cards visibility rules are
// respected.
func TestGameStateHandlerBuildGameStates(t *testing.T) {
	// Build a minimal table snapshot with two players.
	cardA := poker.NewCardFromSuitValue(poker.Spades, poker.Ace)
	cardK := poker.NewCardFromSuitValue(poker.Hearts, poker.King)

	p1Snap := &PlayerSnapshot{
		ID:      "p1",
		Balance: 1000,
		IsReady: true,
		Hand:    []poker.Card{cardA, cardK},
	}
	p2Snap := &PlayerSnapshot{
		ID:      "p2",
		Balance: 1000,
		IsReady: true,
		Hand:    []poker.Card{cardA}, // irrelevant – should be hidden from p1
	}

	gsnap := &GameSnapshot{
		Phase:         pokerrpc.GamePhase_PRE_FLOP,
		Pot:           0,
		CurrentBet:    0,
		CurrentPlayer: "p1",
	}

	tsnap := &TableSnapshot{
		ID:           "tid",
		Players:      []*PlayerSnapshot{p1Snap, p2Snap},
		GameSnapshot: gsnap,
		Config:       poker.TableConfig{MinPlayers: 2},
		State:        TableState{GameStarted: true, PlayerCount: 2},
	}

	gsh := NewGameStateHandler(newBareServer())
	updates := gsh.buildGameStatesFromSnapshot(tsnap)

	if len(updates) != 2 {
		t.Fatalf("expected 2 game updates, got %d", len(updates))
	}

	// p1 should see own cards but not p2's.
	up1 := updates["p1"]
	if up1 == nil {
		t.Fatalf("missing update for p1")
	}
	if len(up1.Players) != 2 {
		t.Fatalf("update for p1 should include 2 players, got %d", len(up1.Players))
	}
	var p1HandVisible, p2HandVisible bool
	for _, pl := range up1.Players {
		switch pl.Id {
		case "p1":
			p1HandVisible = len(pl.Hand) == 2
		case "p2":
			p2HandVisible = len(pl.Hand) > 0
		}
	}
	if !p1HandVisible {
		t.Errorf("p1 should see own hand but it's hidden")
	}
	if p2HandVisible {
		t.Errorf("p1 should NOT see p2 hand in preflop phase")
	}
}

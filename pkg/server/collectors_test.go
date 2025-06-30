package server

import (
	"testing"
	"time"

	"github.com/decred/slog"
	"github.com/vctt94/poker-bisonrelay/pkg/poker"
	"github.com/vctt94/poker-bisonrelay/pkg/server/internal/db"
)

// ---------- Test scaffolding ---------- //
// stubDB is a minimal in-memory implementation of the Database interface used only for these unit tests.
type stubDB struct{}

func (stubDB) GetPlayerBalance(string) (int64, error)                  { return 0, nil }
func (stubDB) UpdatePlayerBalance(string, int64, string, string) error { return nil }
func (stubDB) SaveTableState(*db.TableState) error                     { return nil }
func (stubDB) LoadTableState(string) (*db.TableState, error)           { return nil, nil }
func (stubDB) DeleteTableState(string) error                           { return nil }
func (stubDB) SavePlayerState(string, *db.PlayerState) error           { return nil }
func (stubDB) SaveSnapshot(*db.TableState, []*db.PlayerState) error    { return nil }
func (stubDB) LoadPlayerStates(string) ([]*db.PlayerState, error)      { return nil, nil }
func (stubDB) DeletePlayerState(string, string) error                  { return nil }
func (stubDB) GetAllTableIDs() ([]string, error)                       { return nil, nil }
func (stubDB) Close() error                                            { return nil }

// newBareServer returns a minimal Server suitable for snapshot tests.
func newBareServer() *Server {
	return &Server{
		log:    slog.Disabled,
		db:     stubDB{},
		tables: make(map[string]*poker.Table),
	}
}

// helper to build a 2-player table already in GAME_ACTIVE phase.
func buildActiveHeadsUpTable(t *testing.T, id string) *poker.Table {
	cfg := poker.TableConfig{
		ID:            id,
		Log:           slog.Disabled,
		HostID:        "p1",
		BuyIn:         0,
		MinPlayers:    2,
		MaxPlayers:    2,
		SmallBlind:    10,
		BigBlind:      20,
		StartingChips: 1000,
		TimeBank:      30 * time.Second,
	}

	table := poker.NewTable(cfg)
	table.SetNotificationSender(nil)
	table.SetStateSaver(nil)

	if _, err := table.AddNewUser("p1", "p1", 1000, 0); err != nil {
		t.Fatalf("add user p1: %v", err)
	}
	if _, err := table.AddNewUser("p2", "p2", 1000, 1); err != nil {
		t.Fatalf("add user p2: %v", err)
	}
	if err := table.SetPlayerReady("p1", true); err != nil {
		t.Fatalf("ready p1: %v", err)
	}
	if err := table.SetPlayerReady("p2", true); err != nil {
		t.Fatalf("ready p2: %v", err)
	}
	// advance state machine
	if !table.CheckAllPlayersReady() {
		t.Fatal("table should report PLAYERS_READY")
	}
	if err := table.StartGame(); err != nil {
		t.Fatalf("start game: %v", err)
	}
	return table
}

// ---------- Tests ---------- //

// TestGameSnapshotCurrentBet confirms CurrentBet in snapshot equals table BigBlind right after blinds.
func TestGameSnapshotCurrentBet(t *testing.T) {
	s := newBareServer()
	table := buildActiveHeadsUpTable(t, "table_test")
	s.tables[table.GetConfig().ID] = table

	snap, err := s.collectTableSnapshot(table.GetConfig().ID)
	if err != nil {
		t.Fatalf("snapshot err: %v", err)
	}
	if snap.GameSnapshot == nil {
		t.Fatal("GameSnapshot nil")
	}
	got, want := snap.GameSnapshot.CurrentBet, table.GetConfig().BigBlind
	if got != want {
		t.Fatalf("CurrentBet mismatch: got %d want %d", got, want)
	}
}

// TestCollectGameEventSnapshotInjectsPlayerID verifies helper always sets metadata["playerID"].
func TestCollectGameEventSnapshotInjectsPlayerID(t *testing.T) {
	s := newBareServer()
	event, err := CollectGameEventSnapshot(GameEventTypePlayerJoined, s, "tid", "pid", 0, nil)
	if err != nil {
		t.Fatalf("collect snapshot err: %v", err)
	}
	pid, ok := event.Metadata["playerID"]
	if !ok {
		t.Fatal("playerID key missing in metadata")
	}
	if pid.(string) != "pid" {
		t.Fatalf("playerID mismatch: got %v", pid)
	}
}

func TestPlayerJoinedCollectorNoTable(t *testing.T) {
	s := newBareServer()

	collector := &PlayerJoinedCollector{}
	evt, err := collector.CollectSnapshot(s, "tid", "pid", 0, map[string]interface{}{"message": "joined"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.TableSnapshot != nil {
		t.Errorf("expected nil TableSnapshot when table doesn't exist")
	}
	if len(evt.PlayerIDs) != 1 || evt.PlayerIDs[0] != "pid" {
		t.Errorf("expected PlayerIDs to contain only joining player, got %v", evt.PlayerIDs)
	}
}

// TestCollectTableSnapshotMissingTable ensures an error is returned when trying
// to snapshot a non-existent table.
func TestCollectTableSnapshotMissingTable(t *testing.T) {
	s := newBareServer()
	_, err := s.collectTableSnapshot("unknown")
	if err == nil {
		t.Fatalf("expected error when table is missing, got nil")
	}
}

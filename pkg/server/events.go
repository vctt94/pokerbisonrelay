package server

import (
	"fmt"
	"sync"
	"time"

	"github.com/decred/slog"
	"github.com/vctt94/poker-bisonrelay/pkg/poker"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
)

// GameEventType represents the type of game event
type GameEventType string

const (
	GameEventTypeBetMade      GameEventType = "bet_made"
	GameEventTypePlayerFolded GameEventType = "player_folded"
	GameEventTypeCallMade     GameEventType = "call_made"
	GameEventTypeCheckMade    GameEventType = "check_made"
	GameEventTypeGameStarted  GameEventType = "game_started"
	GameEventTypeGameEnded    GameEventType = "game_ended"
	GameEventTypePlayerReady  GameEventType = "player_ready"
	GameEventTypePlayerJoined GameEventType = "player_joined"
	GameEventTypePlayerLeft   GameEventType = "player_left"
)

// GameEvent represents an immutable snapshot of a game event
type GameEvent struct {
	Type          GameEventType
	TableID       string
	PlayerIDs     []string // All players who should receive updates
	Amount        int64
	Metadata      map[string]interface{}
	Timestamp     time.Time
	TableSnapshot *TableSnapshot
}

// TableSnapshot represents an immutable snapshot of table state
type TableSnapshot struct {
	ID           string
	Players      []*PlayerSnapshot
	GameSnapshot *GameSnapshot
	Config       poker.TableConfig
	State        TableState
	Timestamp    time.Time
}

// PlayerSnapshot represents an immutable snapshot of player state
type PlayerSnapshot struct {
	ID                string
	TableSeat         int
	Balance           int64
	Hand              []poker.Card
	DCRAccountBalance int64
	IsReady           bool
	IsDisconnected    bool
	HasFolded         bool
	IsAllIn           bool
	IsDealer          bool
	IsTurn            bool
	GameState         string
	HandDescription   string
	HasBet            int64
	StartingBalance   int64
}

// GameSnapshot represents an immutable snapshot of game state
type GameSnapshot struct {
	Phase          pokerrpc.GamePhase
	CurrentPlayer  string
	Pot            int64
	CurrentBet     int64
	CommunityCards []poker.Card
	Dealer         int
	Round          int
	BetRound       int
	DeckState      []poker.Card
	Winners        []string
}

// TableState represents table-level state
type TableState struct {
	GameStarted     bool
	AllPlayersReady bool
	PlayerCount     int
}

// EventProcessor manages the processing of game events
type EventProcessor struct {
	server   *Server
	log      slog.Logger
	queue    chan *GameEvent
	workers  []*eventWorker
	stopChan chan struct{}
	wg       sync.WaitGroup
	started  bool
	mu       sync.Mutex
}

// eventWorker processes events from the queue
type eventWorker struct {
	id        int
	processor *EventProcessor
	stopChan  chan struct{}
	wg        *sync.WaitGroup
}

// NewEventProcessor creates a new event processor
func NewEventProcessor(server *Server, queueSize, workerCount int) *EventProcessor {
	processor := &EventProcessor{
		server:   server,
		log:      server.log,
		queue:    make(chan *GameEvent, queueSize),
		stopChan: make(chan struct{}),
	}

	// Create workers
	processor.workers = make([]*eventWorker, workerCount)
	for i := 0; i < workerCount; i++ {
		processor.workers[i] = &eventWorker{
			id:        i,
			processor: processor,
			stopChan:  make(chan struct{}),
			wg:        &processor.wg,
		}
	}

	return processor
}

// Start begins processing events
func (ep *EventProcessor) Start() {
	ep.mu.Lock()
	defer ep.mu.Unlock()

	if ep.started {
		return
	}

	ep.started = true
	ep.log.Infof("Starting event processor with %d workers", len(ep.workers))

	// Start all workers
	for _, worker := range ep.workers {
		ep.wg.Add(1)
		go worker.run()
	}
}

// Stop gracefully stops the event processor
func (ep *EventProcessor) Stop() {
	ep.mu.Lock()
	defer ep.mu.Unlock()

	if !ep.started {
		return
	}

	ep.log.Infof("Stopping event processor...")

	// Signal all workers to stop
	close(ep.stopChan)
	for _, worker := range ep.workers {
		close(worker.stopChan)
	}

	// Wait for all workers to finish
	ep.wg.Wait()

	ep.started = false
	ep.log.Infof("Event processor stopped")
}

// PublishEvent publishes an event for processing
func (ep *EventProcessor) PublishEvent(event *GameEvent) {
	ep.mu.Lock()
	started := ep.started
	ep.mu.Unlock()

	if !started {
		ep.log.Warnf("Event processor not started, dropping event: %v", event.Type)
		return
	}

	select {
	case ep.queue <- event:
		ep.log.Debugf("Published event: %s for table %s", event.Type, event.TableID)
	default:
		ep.log.Errorf("Event queue full, dropping event: %s for table %s", event.Type, event.TableID)
	}
}

// run executes the worker loop
func (w *eventWorker) run() {
	defer w.wg.Done()
	w.processor.log.Debugf("Event worker %d started", w.id)

	for {
		select {
		case <-w.stopChan:
			w.processor.log.Debugf("Event worker %d stopping", w.id)
			return

		case <-w.processor.stopChan:
			w.processor.log.Debugf("Event worker %d stopping (processor shutdown)", w.id)
			return

		case event := <-w.processor.queue:
			if event != nil {
				w.processEvent(event)
			}
		}
	}
}

// processEvent processes a single event using all registered handlers
func (w *eventWorker) processEvent(event *GameEvent) {
	w.processor.log.Debugf("Worker %d processing event: %s for table %s", w.id, event.Type, event.TableID)

	// Process event through all handlers
	w.processNotifications(event)
	w.processGameStateUpdates(event)
	w.processPersistence(event)
}

// processNotifications handles notification broadcasting for the event
func (w *eventWorker) processNotifications(event *GameEvent) {
	handler := NewNotificationHandler(w.processor.server)
	handler.HandleEvent(event)
}

// processGameStateUpdates handles game state broadcasting for the event
func (w *eventWorker) processGameStateUpdates(event *GameEvent) {
	handler := NewGameStateHandler(w.processor.server)
	handler.HandleEvent(event)
}

// processPersistence handles state persistence for the event
func (w *eventWorker) processPersistence(event *GameEvent) {
	handler := NewPersistenceHandler(w.processor.server)
	handler.HandleEvent(event)
}

// SnapshotCollector defines the interface for collecting event snapshots
type SnapshotCollector interface {
	CollectSnapshot(s *Server, tableID, playerID string, amount int64, metadata map[string]interface{}) (*GameEvent, error)
	EventType() GameEventType
}

// SnapshotRegistry manages snapshot collectors
type SnapshotRegistry struct {
	collectors map[GameEventType]SnapshotCollector
	mu         sync.RWMutex
}

// NewSnapshotRegistry creates a new snapshot registry
func NewSnapshotRegistry() *SnapshotRegistry {
	registry := &SnapshotRegistry{
		collectors: make(map[GameEventType]SnapshotCollector),
	}

	// Register all collectors
	registry.Register(&BetMadeCollector{})
	registry.Register(&PlayerFoldedCollector{})
	registry.Register(&CallMadeCollector{})
	registry.Register(&CheckMadeCollector{})
	registry.Register(&GameStartedCollector{})
	registry.Register(&GameEndedCollector{})
	registry.Register(&PlayerReadyCollector{})
	registry.Register(&PlayerJoinedCollector{})
	registry.Register(&PlayerLeftCollector{})

	return registry
}

// Register registers a snapshot collector
func (sr *SnapshotRegistry) Register(collector SnapshotCollector) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.collectors[collector.EventType()] = collector
}

// CollectSnapshot collects a snapshot for the given event type
func (sr *SnapshotRegistry) CollectSnapshot(eventType GameEventType, s *Server, tableID, playerID string, amount int64, metadata map[string]interface{}) (*GameEvent, error) {
	sr.mu.RLock()
	collector, exists := sr.collectors[eventType]
	sr.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no collector registered for event type: %s", eventType)
	}

	return collector.CollectSnapshot(s, tableID, playerID, amount, metadata)
}

// Global snapshot registry
var defaultSnapshotRegistry = NewSnapshotRegistry()

// CollectGameEventSnapshot is a convenience function to collect snapshots
func CollectGameEventSnapshot(eventType GameEventType, s *Server, tableID, playerID string, amount int64, metadata map[string]interface{}) (*GameEvent, error) {
	// Ensure metadata map is initialized and contains the acting player ID. This avoids nil-interface panics
	// when notification handlers perform type assertions.
	if metadata == nil {
		metadata = make(map[string]interface{})
	}
	// Inject only if not already set to avoid overwriting provided override values.
	if _, has := metadata["playerID"]; !has {
		metadata["playerID"] = playerID
	}
	return defaultSnapshotRegistry.CollectSnapshot(eventType, s, tableID, playerID, amount, metadata)
}

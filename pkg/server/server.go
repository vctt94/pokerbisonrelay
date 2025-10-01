package server

import (
	"sync"

	"github.com/decred/slog"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/pokerbisonrelay/pkg/poker"
	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
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
	// Concurrent registry of tables to avoid coarse-grained server locking.
	tables sync.Map // key: string (tableID) -> value: *poker.Table

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

// getTable retrieves a table by ID from the registry.
func (s *Server) getTable(tableID string) (*poker.Table, bool) {
	if v, ok := s.tables.Load(tableID); ok {
		if t, ok2 := v.(*poker.Table); ok2 && t != nil {
			return t, true
		}
	}
	return nil, false
}

func (s *Server) getAllTables() []*poker.Table {
	tableRefs := make([]*poker.Table, 0)
	s.tables.Range(func(_, value any) bool {
		if t, ok := value.(*poker.Table); ok && t != nil {
			tableRefs = append(tableRefs, t)
		}
		return true
	})
	return tableRefs
}

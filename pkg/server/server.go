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

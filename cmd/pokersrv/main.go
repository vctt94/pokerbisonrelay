package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	_ "github.com/mattn/go-sqlite3"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/poker-bisonrelay/pkg/server"
	"google.golang.org/grpc"
)

func main() {
	var (
		dbPath      string
		host        string
		port        int
		portFile    string
		seed        int64
		autoStartMs int
		debugLevel  string
	)
	flag.StringVar(&dbPath, "db", "", "Path to SQLite database file (created if missing)")
	flag.StringVar(&host, "host", "127.0.0.1", "Host to listen on")
	flag.IntVar(&port, "port", 0, "Port to listen on (0 for random free port)")
	flag.StringVar(&portFile, "portfile", "", "If set, write selected port to this file")
	flag.Int64Var(&seed, "seed", 0, "Deterministic RNG seed for decks (0 = random)")
	flag.IntVar(&autoStartMs, "autostartms", 0, "Auto-start delay between hands in milliseconds (0 = server default)")
	flag.StringVar(&debugLevel, "debuglevel", "info", "Logging level: trace, debug, info, warn, error")
	flag.Parse()

	if dbPath == "" {
		// Default to temp dir
		tmp := os.TempDir()
		dbPath = filepath.Join(tmp, "poker_e2e.sqlite")
	}

	// Init DB
	db, err := server.NewDatabase(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to init db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Logging backend
	logBackend, _ := logging.NewLogBackend(logging.LogConfig{DebugLevel: debugLevel})

	// Create server
	pokerSrv := server.NewServer(db, logBackend)
	if seed == 0 {
		// Allow env override for convenience
		if env := os.Getenv("POKER_SEED"); env != "" {
			if v, err := strconv.ParseInt(env, 10, 64); err == nil {
				seed = v
			}
		}
	}

	// Insecure gRPC for local testing
	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to listen: %v\n", err)
		os.Exit(1)
	}

	grpcSrv := grpc.NewServer()
	pokerrpc.RegisterLobbyServiceServer(grpcSrv, pokerSrv)
	pokerrpc.RegisterPokerServiceServer(grpcSrv, pokerSrv)

	// Optionally write chosen port
	if portFile != "" {
		_, p, _ := net.SplitHostPort(lis.Addr().String())
		_ = os.WriteFile(portFile, []byte(p), 0600)
	}

	// Serve (blocking)
	if err := grpcSrv.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "grpc serve error: %v\n", err)
		os.Exit(1)
	}
}

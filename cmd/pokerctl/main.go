package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vctt94/poker-bisonrelay/pkg/client"
	"github.com/vctt94/poker-bisonrelay/pkg/poker"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
)

// Common flags
var (
	dataDir         = flag.String("datadir", "", "Directory to load config file from")
	rpcURL          = flag.String("url", "", "URL of the websocket endpoint")
	grpcServerCert  = flag.String("grpcservercert", "", "Path to server.crt file for TLS")
	brClientCert    = flag.String("brclientcert", "", "Path to brclient rpc.cert file")
	brClientRPCCert = flag.String("brclientrpc.cert", "", "Path to rpc-client.cert file")
	brClientRPCKey  = flag.String("brclientrpc.key", "", "Path to rpc-client.key file")
	rpcUser         = flag.String("rpcuser", "", "RPC user for basic authentication")
	rpcPass         = flag.String("rpcpass", "", "RPC password for basic authentication")
	grpcHost        = flag.String("grpchost", "", "GRPC server hostname")
	grpcPort        = flag.String("grpcport", "", "GRPC server port")
	logFile         = flag.String("logfile", "", "Path to log file")
	maxLogFiles     = flag.Int("maxlogfiles", 10, "Maximum number of log files")
	maxBufferLines  = flag.Int("maxbufferlines", 1000, "Maximum number of buffer lines")
	debug           = flag.String("debug", "", "Debug level for logging")
	grpcInsecure    = flag.Bool("grpcinsecure", false, "Use insecure gRPC (no TLS) - tests only")
	offline         = flag.Bool("offline", false, "Skip BisonRelay init (tests only)")
	playerID        = flag.String("id", "", "Explicit player ID (offline mode)")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [global flags] <command> [args]\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  id                               Show player ID")
		fmt.Fprintln(os.Stderr, "  balance [--add N]                Show or add to balance")
		fmt.Fprintln(os.Stderr, "  tables                           List tables (JSON)")
		fmt.Fprintln(os.Stderr, "  create-table [opts]              Create table; prints table ID")
		fmt.Fprintln(os.Stderr, "  join --table-id ID               Join a table")
		fmt.Fprintln(os.Stderr, "  leave                            Leave current table")
		fmt.Fprintln(os.Stderr, "  ready set|unset                  Set or unset ready state")
		fmt.Fprintln(os.Stderr, "  state [--table-id ID]            Print game state (JSON)")
		fmt.Fprintln(os.Stderr, "  stream [--table-id ID]           Stream game updates (JSON)")
		fmt.Fprintln(os.Stderr, "  act check|call|bet N|raise N|fold Perform an action")
		fmt.Fprintln(os.Stderr, "  autoplay-one-hand                Auto check/call until showdown")
		fmt.Fprintln(os.Stderr, "  last-winners [--table-id ID]     Print last hand winners (JSON)")
		fmt.Fprintln(os.Stderr, "\nGlobal flags:")
		flag.PrintDefaults()
	}

	// Suppress default flag errors to avoid noisy usage on subcommands
	flag.CommandLine.SetOutput(io.Discard)
	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}

	cmd := flag.Arg(0)

	cfg := &client.PokerClientConfig{}
	if err := cfg.LoadConfig("pokerclient", *dataDir); err != nil {
		fmt.Printf("Configuration error: %v\n", err)
		os.Exit(1)
	}

	flagOverrides := make(map[string]interface{})
	if *rpcURL != "" {
		flagOverrides["rpcurl"] = *rpcURL
	}
	if *grpcServerCert != "" {
		flagOverrides["grpcservercert"] = *grpcServerCert
	}
	if *brClientCert != "" {
		flagOverrides["brclientcert"] = *brClientCert
	}
	if *brClientRPCCert != "" {
		flagOverrides["brclientrpccert"] = *brClientRPCCert
	}
	if *brClientRPCKey != "" {
		flagOverrides["brclientrpckey"] = *brClientRPCKey
	}
	if *rpcUser != "" {
		flagOverrides["rpcuser"] = *rpcUser
	}
	if *rpcPass != "" {
		flagOverrides["rpcpass"] = *rpcPass
	}
	if *grpcHost != "" {
		flagOverrides["grpchost"] = *grpcHost
	}
	if *grpcPort != "" {
		flagOverrides["grpcport"] = *grpcPort
	}
	if *logFile != "" {
		flagOverrides["logfile"] = *logFile
	}
	if *maxLogFiles != 10 {
		flagOverrides["maxlogfiles"] = *maxLogFiles
	}
	if *maxBufferLines != 1000 {
		flagOverrides["maxbufferlines"] = *maxBufferLines
	}
	if *debug != "" {
		flagOverrides["debug"] = *debug
	}
	if *grpcInsecure {
		flagOverrides["grpcinsecure"] = true
	}
	if *offline {
		flagOverrides["offline"] = true
	}
	if *playerID != "" {
		flagOverrides["id"] = *playerID
	}
	cfg.SetConfigValues(flagOverrides)

	// Minimal validation for GRPC connectivity
	if cfg.GRPCHost == "" || cfg.GRPCPort == "" {
		fmt.Println("grpchost and grpcport are required (from config file or flags)")
		os.Exit(1)
	}

	// Initialize notification manager (required by PokerClient)
	cfg.Notifications = client.NewNotificationManager()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pcli, err := client.NewPokerClient(ctx, cfg)
	if err != nil {
		fmt.Printf("Failed to create poker client: %v\n", err)
		os.Exit(1)
	}
	defer pcli.Close()

	switch cmd {
	case "id":
		fmt.Println(pcli.ID)
		return

	case "balance":
		if err := handleBalance(ctx, pcli, flag.Args()[1:]); err != nil {
			fatalErr(err)
		}
		return

	case "tables":
		if err := handleTables(ctx, pcli); err != nil {
			fatalErr(err)
		}
		return

	case "create-table":
		if err := handleCreateTable(ctx, pcli, flag.Args()[1:]); err != nil {
			fatalErr(err)
		}
		return

	case "join":
		if err := handleJoin(ctx, pcli, flag.Args()[1:]); err != nil {
			fatalErr(err)
		}
		return

	case "leave":
		if err := pcli.LeaveTable(ctx); err != nil {
			fatalErr(err)
		}
		return

	case "ready":
		if err := handleReady(ctx, pcli, flag.Args()[1:]); err != nil {
			fatalErr(err)
		}
		return

	case "state":
		if err := handleState(ctx, pcli, flag.Args()[1:]); err != nil {
			fatalErr(err)
		}
		return

	case "stream":
		if err := handleStream(ctx, pcli, flag.Args()[1:]); err != nil {
			fatalErr(err)
		}
		return

	case "act":
		if err := handleAct(ctx, pcli, flag.Args()[1:]); err != nil {
			fatalErr(err)
		}
		return

	case "autoplay-one-hand":
		if err := handleAutoplayOneHand(ctx, pcli, flag.Args()[1:]); err != nil {
			fatalErr(err)
		}
		return

	case "last-winners":
		if err := handleLastWinners(ctx, pcli, flag.Args()[1:]); err != nil {
			fatalErr(err)
		}
		return

	default:
		flag.Usage()
		os.Exit(2)
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

func fatalErr(err error) {
	fatal(err.Error())
}

func handleReady(ctx context.Context, pcli *client.PokerClient, args []string) error {
	fs := flag.NewFlagSet("ready", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tableID := fs.String("table-id", "", "Table ID")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("ready: %w", err)
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("ready requires set|unset")
	}
	if *tableID != "" {
		if err := pcli.JoinTable(ctx, *tableID); err != nil {
			return err
		}
	}
	switch rest[0] {
	case "set":
		return pcli.SetPlayerReady(ctx)
	case "unset":
		return pcli.SetPlayerUnready(ctx)
	default:
		return fmt.Errorf("ready requires set|unset")
	}
}

func handleBalance(ctx context.Context, pcli *client.PokerClient, args []string) error {
	addIdx := indexOf(args, "--add")
	if addIdx >= 0 {
		if addIdx+1 >= len(args) {
			return errors.New("--add requires amount")
		}
		n, err := strconv.ParseInt(args[addIdx+1], 10, 64)
		if err != nil {
			return err
		}
		newBal, err := pcli.UpdateBalance(ctx, n, "pokerctl add")
		if err != nil {
			return err
		}
		fmt.Println(newBal)
		return nil
	}
	b, err := pcli.GetBalance(ctx)
	if err != nil {
		return err
	}
	fmt.Println(b)
	return nil
}

func handleTables(ctx context.Context, pcli *client.PokerClient) error {
	tables, err := pcli.GetTables(ctx)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(tables)
}

func handleCreateTable(ctx context.Context, pcli *client.PokerClient, args []string) error {
	// Use sub-FlagSet to avoid global flag confusion
	fs := flag.NewFlagSet("create-table", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	smallBlind := fs.Int64("small-blind", 5, "Small blind")
	bigBlind := fs.Int64("big-blind", 10, "Big blind")
	minPlayers := fs.Int("min-players", 2, "Min players")
	maxPlayers := fs.Int("max-players", 2, "Max players")
	buyIn := fs.Int64("buy-in", 0, "Buy-in")
	minBalance := fs.Int64("min-balance", 0, "Min balance")
	startingChips := fs.Int64("starting-chips", 1000, "Starting chips")
	timeBank := fs.Int("time-bank-seconds", 0, "Player timebank in seconds (0=default)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("create-table: %w", err)
	}

	cfg := poker.TableConfig{
		SmallBlind:    *smallBlind,
		BigBlind:      *bigBlind,
		MinPlayers:    *minPlayers,
		MaxPlayers:    *maxPlayers,
		BuyIn:         *buyIn,
		MinBalance:    *minBalance,
		StartingChips: *startingChips,
		TimeBank:      time.Duration(*timeBank) * time.Second,
	}

	id, err := pcli.CreateTable(ctx, cfg)
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

func handleJoin(ctx context.Context, pcli *client.PokerClient, args []string) error {
	fs := flag.NewFlagSet("join", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tableID := fs.String("table-id", "", "Table ID")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("join: %w", err)
	}
	if *tableID == "" {
		return errors.New("join: --table-id is required")
	}
	return pcli.JoinTable(ctx, *tableID)
}

func handleState(ctx context.Context, pcli *client.PokerClient, args []string) error {
	fs := flag.NewFlagSet("state", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tableID := fs.String("table-id", "", "Table ID")
	noDCR := fs.Bool("no-dcr", true, "Do not include DCR balances in per-hand state output")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("state: %w", err)
	}
	id := *tableID
	if id == "" {
		id = pcli.GetCurrentTableID()
		if id == "" {
			return errors.New("state: no table-id provided and not joined to a table")
		}
	}
	resp, err := pcli.PokerService.GetGameState(ctx, &pokerrpc.GetGameStateRequest{TableId: id})
	if err != nil {
		return err
	}

	// Annotate players with stack alias; optionally include DCR balance.
	// - players[].balance in GameUpdate is the in-game poker chip stack.
	var raw map[string]interface{}
	if b, err := json.Marshal(resp.GameState); err == nil {
		_ = json.Unmarshal(b, &raw)
	}
	if raw != nil {
		if ps, ok := raw["players"].([]interface{}); ok {
			for i, pi := range ps {
				pm, ok := pi.(map[string]interface{})
				if !ok {
					continue
				}
				// Add stack alias for clarity
				if bal, ok := pm["balance"]; ok {
					pm["stack"] = bal
				}
				// Optionally fetch and add DCR account balance (disabled by default)
				if !*noDCR {
					if pid, ok := pm["id"].(string); ok && pid != "" {
						if balResp, err := pcli.LobbyService.GetBalance(ctx, &pokerrpc.GetBalanceRequest{PlayerId: pid}); err == nil {
							pm["dcr_balance"] = balResp.Balance
						}
					}
				}
				ps[i] = pm
			}
			raw["players"] = ps
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if raw != nil {
		return enc.Encode(raw)
	}
	return enc.Encode(resp.GameState)
}

func handleStream(ctx context.Context, pcli *client.PokerClient, args []string) error {
	fs := flag.NewFlagSet("stream", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tableID := fs.String("table-id", "", "Table ID")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("stream: %w", err)
	}
	if *tableID != "" {
		if err := pcli.JoinTable(ctx, *tableID); err != nil {
			return err
		}
	}
	if pcli.GetCurrentTableID() == "" {
		return errors.New("join a table first or pass --table-id")
	}
	if err := pcli.StartGameStream(ctx); err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	for {
		select {
		case msg := <-pcli.UpdatesCh:
			if gu, ok := (any)(msg).(client.GameUpdateMsg); ok {
				if err := enc.Encode((*pokerrpc.GameUpdate)(gu)); err != nil {
					return err
				}
			}
		case err := <-pcli.ErrorsCh:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func handleAct(ctx context.Context, pcli *client.PokerClient, args []string) error {
	fs := flag.NewFlagSet("act", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var tableID string
	fs.StringVar(&tableID, "table-id", "", "Table ID")
	fs.StringVar(&tableID, "t", "", "Table ID (shorthand)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("act: %w", err)
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return errors.New("act requires a subcommand")
	}
	if tableID != "" {
		pcli.SetCurrentTableID(tableID)
	} else if pcli.GetCurrentTableID() == "" {
		if tid, err := pcli.GetPlayerCurrentTable(ctx); err == nil && tid != "" {
			pcli.SetCurrentTableID(tid)
		}
	}

	sub := rest[0]
	switch sub {
	case "check":
		return pcli.Check(ctx)
	case "fold":
		return pcli.Fold(ctx)
	case "call":
		gs, err := pcli.PokerService.GetGameState(ctx, &pokerrpc.GetGameStateRequest{TableId: pcli.GetCurrentTableID()})
		if err != nil {
			return err
		}
		return pcli.Call(ctx, gs.GameState.CurrentBet)
	case "bet", "raise":
		if len(rest) < 2 {
			return errors.New("bet/raise requires amount")
		}
		amt := mustAtoi64(rest[1])
		return pcli.Bet(ctx, amt)
	default:
		return fmt.Errorf("unknown act subcommand: %s", sub)
	}
}

func handleAutoplayOneHand(ctx context.Context, pcli *client.PokerClient, args []string) error {
	fs := flag.NewFlagSet("autoplay-one-hand", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tableID := fs.String("table-id", "", "Table ID")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("autoplay-one-hand: %w", err)
	}
	if *tableID != "" {
		if err := pcli.JoinTable(ctx, *tableID); err != nil {
			return err
		}
	}
	if pcli.GetCurrentTableID() == "" {
		return errors.New("join a table first")
	}
	// Start stream
	if err := pcli.StartGameStream(ctx); err != nil {
		return err
	}
	deadline := time.NewTimer(4 * time.Minute)
	defer deadline.Stop()

	for {
		select {
		case <-deadline.C:
			return errors.New("autoplay timeout")
		case msg := <-pcli.UpdatesCh:
			gu, ok := (any)(msg).(client.GameUpdateMsg)
			if !ok || gu == nil {
				continue
			}
			u := (*pokerrpc.GameUpdate)(gu)
			if u.GameStarted && u.Phase == pokerrpc.GamePhase_SHOWDOWN {
				// Slight delay to allow server to settle
				time.Sleep(500 * time.Millisecond)
				return nil
			}
			if u.CurrentPlayer == pcli.ID {
				if u.CurrentBet > 0 {
					_ = pcli.Call(ctx, u.CurrentBet)
				} else {
					_ = pcli.Check(ctx)
				}
				// avoid spamming actions
				time.Sleep(200 * time.Millisecond)
			}
		case err := <-pcli.ErrorsCh:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func handleLastWinners(ctx context.Context, pcli *client.PokerClient, args []string) error {
	fs := flag.NewFlagSet("last-winners", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tableID := fs.String("table-id", "", "Table ID")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("last-winners: %w", err)
	}
	id := *tableID
	if id == "" {
		id = pcli.GetCurrentTableID()
		if id == "" {
			return errors.New("last-winners: no table-id provided and not joined to a table")
		}
	}
	resp, err := pcli.PokerService.GetLastWinners(ctx, &pokerrpc.GetLastWinnersRequest{TableId: id})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(resp)
}

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}

func valueAfter(args []string, key string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == key {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if strings.HasPrefix(arg, key+"=") {
			return strings.TrimPrefix(arg, key+"=")
		}
	}
	return ""
}

func splitKV(arg string) (string, string) {
	arg = strings.TrimPrefix(arg, "--")
	if kv := strings.SplitN(arg, "=", 2); len(kv) == 2 {
		return kv[0], kv[1]
	}
	return arg, ""
}

func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		fatalErr(err)
	}
	return n
}

func mustAtoi64(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		fatalErr(err)
	}
	return n
}

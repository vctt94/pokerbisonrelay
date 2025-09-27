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

	"github.com/vctt94/pokerbisonrelay/pkg/client"
	"github.com/vctt94/pokerbisonrelay/pkg/poker"
	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
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
		fmt.Fprintln(os.Stderr, "  ready set|unset [--table-id ID]  Set or unset ready state")
		fmt.Fprintln(os.Stderr, "  state [--table-id ID]            Print game state (JSON)")
		fmt.Fprintln(os.Stderr, "  stream [--table-id ID]           Stream game updates (JSON)")
		fmt.Fprintln(os.Stderr, "  events [--table-id ID] [--types T1,T2]  Stream server events (notifications) as JSON")
		fmt.Fprintln(os.Stderr, "  wait --type T [--table-id ID] [--timeout D]  Block until event arrives; print it as JSON")
		fmt.Fprintln(os.Stderr, "  act check|call|bet N|raise N|fold [--table-id ID]  Perform an action")
		fmt.Fprintln(os.Stderr, "  last-winners [--table-id ID]     Print last hand winners (JSON)")
		fmt.Fprintln(os.Stderr, "\nGlobal flags:")
		flag.PrintDefaults()
	}

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

	case "events":
		if err := handleEvents(ctx, pcli, flag.Args()[1:]); err != nil {
			fatalErr(err)
		}
		return

	case "wait":
		if err := handleWait(ctx, pcli, flag.Args()[1:]); err != nil {
			fatalErr(err)
		}
		return

	case "act":
		if err := handleAct(ctx, pcli, flag.Args()[1:]); err != nil {
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
	autoStartMs := fs.Int("auto-start-ms", 0, "Auto-start delay between hands in ms (0=disabled)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("create-table: %w", err)
	}

	cfg := poker.TableConfig{
		SmallBlind:     *smallBlind,
		BigBlind:       *bigBlind,
		MinPlayers:     *minPlayers,
		MaxPlayers:     *maxPlayers,
		BuyIn:          *buyIn,
		MinBalance:     *minBalance,
		StartingChips:  *startingChips,
		TimeBank:       time.Duration(*timeBank) * time.Second,
		AutoStartDelay: time.Duration(*autoStartMs) * time.Millisecond,
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

	// Re-label balance->stack and optionally add DCR account balance.
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
				if bal, ok := pm["balance"]; ok {
					pm["stack"] = bal
				}
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

// --- Events (Notifications) ---

func handleEvents(ctx context.Context, pcli *client.PokerClient, args []string) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tableID := fs.String("table-id", "", "Table ID to filter (optional)")
	typesCSV := fs.String("types", "", "Comma-separated event types to include (e.g. SHOWDOWN_RESULT,NEW_HAND_STARTED)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("events: %w", err)
	}
	typeFilter := parseTypes(*typesCSV)

	// NOTE: adjust these to your client API
	if err := pcli.StartNotificationStream(ctx); err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	for {
		select {
		case n := <-pcli.NotificationsCh: // NOTE: channel name in your client
			if n == nil {
				continue
			}
			if *tableID != "" && n.TableId != *tableID {
				continue
			}
			if len(typeFilter) > 0 && !typeFilter[n.Type.String()] {
				continue
			}
			if err := enc.Encode(n); err != nil {
				return err
			}
		case err := <-pcli.ErrorsCh:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func gameStarted(ctx context.Context, pcli *client.PokerClient, tableID string) (bool, *pokerrpc.GameUpdate, error) {
	resp, err := pcli.PokerService.GetGameState(ctx, &pokerrpc.GetGameStateRequest{TableId: tableID})
	if err != nil {
		return false, nil, err
	}
	if resp == nil || resp.GameState == nil {
		return false, nil, nil
	}
	return resp.GameState.GameStarted, resp.GameState, nil
}

func handleWait(ctx context.Context, pcli *client.PokerClient, args []string) error {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	wantType := fs.String("type", "", "Event type to wait for (e.g. SHOWDOWN_RESULT, NEW_HAND_STARTED)")
	tableID := fs.String("table-id", "", "Filter by table ID (optional)")
	timeoutStr := fs.String("timeout", "2m", "Timeout (e.g. 30s, 2m, 5m)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("wait: %w", err)
	}
	if *wantType == "" {
		return errors.New("wait: --type is required")
	}
	to, err := time.ParseDuration(*timeoutStr)
	if err != nil {
		return fmt.Errorf("wait: invalid --timeout: %w", err)
	}
	// Map string -> enum once
	var want pokerrpc.NotificationType
	if v, ok := pokerrpc.NotificationType_value[*wantType]; ok {
		want = pokerrpc.NotificationType(v)
	} else {
		want = parseNotificationTypeRelaxed(*wantType)
		if want == pokerrpc.NotificationType_UNKNOWN {
			return fmt.Errorf("wait: unknown event type %q", *wantType)
		}
	}

	// --- PRECHECK for GAME_STARTED (race-safe) ---
	if want == pokerrpc.NotificationType_GAME_STARTED {
		if *tableID == "" {
			// best effort: derive current table
			tid := pcli.GetCurrentTableID()
			if tid != "" {
				*tableID = tid
			}
		}
		if *tableID != "" {
			started, _, err := gameStarted(ctx, pcli, *tableID)
			if err == nil && started {
				// Emit a synthetic notification payload so callers get JSON they expect
				n := &pokerrpc.Notification{
					Type:    pokerrpc.NotificationType_GAME_STARTED,
					TableId: *tableID,
					Message: "precheck: game already started",
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(n)
			}
		}
	}

	// Start the notification stream and wait
	if err := pcli.StartNotificationStream(ctx); err != nil {
		return err
	}
	timer := time.NewTimer(to)
	defer timer.Stop()

	for {
		select {
		case n := <-pcli.NotificationsCh:
			if n == nil {
				continue
			}
			if n.Type != want {
				continue
			}
			if *tableID != "" && n.TableId != *tableID {
				continue
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(n)

		case err := <-pcli.ErrorsCh:
			return err

		case <-timer.C:
			// --- POSTCHECK on timeout (covers just-missed events) ---
			if want == pokerrpc.NotificationType_GAME_STARTED && *tableID != "" {
				if ok, _, err := gameStarted(ctx, pcli, *tableID); err == nil && ok {
					n := &pokerrpc.Notification{
						Type:    pokerrpc.NotificationType_GAME_STARTED,
						TableId: *tableID,
						Message: "postcheck: game is started",
					}
					enc := json.NewEncoder(os.Stdout)
					enc.SetIndent("", "  ")
					return enc.Encode(n)
				}
			}
			return errors.New("wait: timeout")

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// --- Actions ---

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

	switch rest[0] {
	case "check":
		return pcli.Check(ctx)
	case "fold":
		return pcli.Fold(ctx)
	case "call":
		// Event-directed: let the server figure the amount from state.
		return pcli.Call(ctx, 0)
	case "bet", "raise":
		if len(rest) < 2 {
			return errors.New("bet/raise requires amount")
		}
		amt := mustAtoi64(rest[1])
		return pcli.Bet(ctx, amt)
	default:
		return fmt.Errorf("unknown act subcommand: %s", rest[0])
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

// --- Helpers ---

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}

func mustAtoi64(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		fatalErr(err)
	}
	return n
}

func parseTypes(csv string) map[string]bool {
	if csv == "" {
		return nil
	}
	set := make(map[string]bool)
	for _, t := range strings.Split(csv, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		set[t] = true
	}
	return set
}

func parseNotificationTypeRelaxed(s string) pokerrpc.NotificationType {
	s = strings.TrimSpace(strings.ToUpper(strings.ReplaceAll(s, " ", "_")))
	for k, v := range pokerrpc.NotificationType_value {
		if strings.EqualFold(k, s) {
			return pokerrpc.NotificationType(v)
		}
	}
	// try matching by suffix or partials (e.g., "SHOWDOWN_RESULT" vs "SHOWDOWN")
	for k, v := range pokerrpc.NotificationType_value {
		ku := strings.ToUpper(k)
		if strings.Contains(ku, s) || strings.Contains(s, ku) {
			return pokerrpc.NotificationType(v)
		}
	}
	return pokerrpc.NotificationType_UNKNOWN
}

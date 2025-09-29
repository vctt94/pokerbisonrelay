package golib

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/companyzero/bisonrelay/clientrpc/types"
	"github.com/companyzero/bisonrelay/lockfile"
	"github.com/companyzero/bisonrelay/rates"
	"github.com/companyzero/bisonrelay/zkidentity"
	"github.com/decred/slog"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/pokerbisonrelay/pkg/client"
	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
)

type initClient struct {
	ServerAddr string `json:"server_addr"`

	GRPCCertPath   string `json:"grpc_cert_path"`
	PayoutAddress  string `json:"payout_address"`
	DBRoot         string `json:"dbroot"`
	DataDir        string `json:"datadir"`
	DownloadsDir   string `json:"downloads_dir"`
	LogFile        string `json:"log_file"`
	DebugLevel     string `json:"debug_level"`
	WantsLogNtfns  bool   `json:"wants_log_ntfns"`
	LogPings       bool   `json:"log_pings"`
	PingIntervalMs int64  `json:"ping_interval_ms"`

	// New fields for RPC configuration
	RPCWebsocketURL   string `json:"rpc_websocket_url"`
	RPCCertPath       string `json:"rpc_cert_path"`
	RPCCLientCertPath string `json:"rpc_client_cert_path"`
	RPCCLientKeyPath  string `json:"rpc_client_key_path"`
	RPCUser           string `json:"rpc_user"`
	RPCPass           string `json:"rpc_pass"`
}

type initPokerClient struct {
	DataDir        string `json:"datadir"`
	GRPCHost       string `json:"grpc_host"`
	GRPCPort       string `json:"grpc_port"`
	GRPCServerCert string `json:"grpc_server_cert"`
	Insecure       bool   `json:"insecure"`
	Offline        bool   `json:"offline"`
	PlayerID       string `json:"player_id,omitempty"`
	LogFile        string `json:"log_file"`
	DebugLevel     string `json:"debug_level"`
}

type createDefaultConfigArgs struct {
	DataDir         string `json:"datadir"`
	ServerAddr      string `json:"server_addr"`
	GRPCCertPath    string `json:"grpc_cert_path"`
	DebugLevel      string `json:"debug_level"`
	BrRpcUrl        string `json:"br_rpc_url"`
	BrClientCert    string `json:"br_client_cert"`
	BrClientRpcCert string `json:"br_client_rpc_cert"`
	BrClientRpcKey  string `json:"br_client_rpc_key"`
	RpcUser         string `json:"rpc_user"`
	RpcPass         string `json:"rpc_pass"`
}

// JSON payloads from Flutter
type joinWaitingRoom struct {
	RoomID   string `json:"room_id"`
	EscrowId string `json:"escrow_id"` // optional
}

type createWaitingRoom struct {
	ClientID string `json:"client_id"`
	BetAmt   int64  `json:"bet_amt"`
	EscrowId string `json:"escrow_id"` // optional
}

type openEscrowReq struct {
	Payout    string `json:"payout"`
	BetAtoms  int64  `json:"bet_atoms"`
	CSVBlocks int64  `json:"csv_blocks"`
}

type preSignReq struct {
	MatchID string `json:"match_id"`
}

type joinPokerTable struct {
	TableID string `json:"table_id"`
}

type createPokerTable struct {
	SmallBlind      int64 `json:"small_blind"`
	BigBlind        int64 `json:"big_blind"`
	MaxPlayers      int32 `json:"max_players"`
	MinPlayers      int32 `json:"min_players"`
	MinBalance      int64 `json:"min_balance"`
	BuyIn           int64 `json:"buy_in"`
	StartingChips   int64 `json:"starting_chips"`
	TimeBankSeconds int32 `json:"time_bank_seconds"`
	AutoStartMs     int32 `json:"auto_start_ms"`
}

// JSON returned to Flutter (shape must match Dart LocalWaitingRoom/LocalPlayer)
type player struct {
	UID    string `json:"uid"`
	Nick   string `json:"nick,omitempty"`
	BetAmt int64  `json:"bet_amt"`
	Ready  bool   `json:"ready"`
}

type waitingRoom struct {
	ID      string    `json:"id"`
	HostID  string    `json:"host_id"`
	BetAmt  int64     `json:"bet_amt"`
	Players []*player `json:"players,omitempty"`
}

func playerFromServer(sp *pokerrpc.Player) (*player, error) {
	// Adjust to your actual type/fields.
	return &player{
		UID:    sp.Id,
		Nick:   sp.Name,
		BetAmt: sp.Balance,
		Ready:  sp.IsReady,
	}, nil
}

// localInfo represents local client information
type localInfo struct {
	ID   zkidentity.ShortID `json:"id"`
	Nick string             `json:"nick"`
}

// runState represents the current run state
type runState struct {
	ClientRunning bool `json:"client_running"`
}

// escrowState represents escrow information
type escrowState struct {
	EscrowId       string `json:"escrow_id"`
	DepositAddress string `json:"deposit_address"`
	PkScriptHex    string `json:"pk_script_hex"`
}

// clientCtx represents a client context
type clientCtx struct {
	ID     *localInfo
	c      *client.PokerClient
	ctx    context.Context
	chat   types.ChatServiceClient
	cancel func()
	runMtx sync.Mutex
	runErr error

	log          slog.Logger
	certConfChan chan bool

	httpClient *http.Client
	rates      *rates.Rates

	// expirationDays are the expiration days provided by the server when
	// connected
	expirationDays uint64

	serverState atomic.Value
}

// Global variables
var (
	cmtx sync.Mutex
	cs   map[uint32]*clientCtx
	lfs  map[string]*lockfile.LockFile = map[string]*lockfile.LockFile{}

	// The following are debug vars.
	sigUrgCount       atomic.Uint64
	isServerConnected atomic.Bool

	// Global escrow state for demo purposes
	es *escrowState
)

// parseJoinWRPayload parses the join waiting room payload
func parseJoinWRPayload(payload []byte) (roomID, escrowID string, err error) {
	var req joinWaitingRoom
	if err := json.Unmarshal(payload, &req); err != nil {
		return "", "", fmt.Errorf("unmarshal join WR payload: %w", err)
	}
	return req.RoomID, req.EscrowId, nil
}

// handleInitClient initializes a new client with proper configuration
func handleInitClient(handle uint32, args initClient) (*localInfo, error) {
	cmtx.Lock()
	defer cmtx.Unlock()
	if cs == nil {
		cs = make(map[uint32]*clientCtx)
	}
	if cs[handle] != nil {
		return cs[handle].ID, nil
	}

	// Ensure the data directory exists first
	if err := os.MkdirAll(args.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory %s: %v", args.DataDir, err)
	}

	// Ensure the logs subdirectory exists
	logsDir := filepath.Dir(args.LogFile)
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create logs directory %s: %v", logsDir, err)
	}

	// Load configuration with any overrides supplied by the Flutter shell
	overrides := client.ConfigOverrides{
		BRClientRPCURL:  args.RPCWebsocketURL,
		BRClientCert:    args.RPCCertPath,
		BRClientRPCCert: args.RPCCLientCertPath,
		BRClientRPCKey:  args.RPCCLientKeyPath,
		RPCUser:         args.RPCUser,
		RPCPass:         args.RPCPass,
		GRPCServerCert:  args.GRPCCertPath,
		PayoutAddress:   args.PayoutAddress,
	}
	cfg, err := client.LoadAppConfig(args.DataDir, overrides)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %v", err)
	}

	// Apply overrides from args when available
	flagOverrides := make(map[string]interface{})
	if args.RPCWebsocketURL != "" {
		flagOverrides["brrpcurl"] = args.RPCWebsocketURL
	}
	if args.RPCCertPath != "" {
		flagOverrides["brclientcert"] = args.RPCCertPath
	}
	if args.RPCCLientCertPath != "" {
		flagOverrides["brclientrpccert"] = args.RPCCLientCertPath
	}
	if args.RPCCLientKeyPath != "" {
		flagOverrides["brclientrpckey"] = args.RPCCLientKeyPath
	}
	if args.RPCUser != "" {
		flagOverrides["rpcuser"] = args.RPCUser
	}
	if args.RPCPass != "" {
		flagOverrides["rpcpass"] = args.RPCPass
	}
	if args.DebugLevel != "" {
		flagOverrides["debug"] = args.DebugLevel
	}
	if args.ServerAddr != "" {
		// Parse server address for GRPC host and port
		parts := strings.Split(args.ServerAddr, ":")
		if len(parts) >= 2 {
			flagOverrides["grpchost"] = parts[0]
			flagOverrides["grpcport"] = parts[1]
		}
	}
	if args.GRPCCertPath != "" {
		flagOverrides["grpcservercert"] = args.GRPCCertPath
	}

	cfg.SetConfigValues(flagOverrides)

	// Validate configuration
	if err := cfg.ValidateConfig(); err != nil {
		return nil, fmt.Errorf("configuration validation error: %v", err)
	}
	fmt.Println("cfg", cfg)

	// Initialize notification manager BEFORE creating the client
	cfg.Notifications = client.NewNotificationManager()

	// Create context
	ctx, cancel := context.WithCancel(context.Background())

	// Create poker client with configuration
	pokerClient, err := client.NewPokerClient(ctx, cfg)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create poker client: %v", err)
	}

	// Start the notification stream
	if err := pokerClient.StartNotificationStream(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start notifications: %v", err)
	}

	// Initialize clientID using a random 32-byte value (avoid BR dependency for ID)
	var rnd [32]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to generate random id: %v", err)
	}
	var id zkidentity.ShortID
	id.FromBytes(rnd[:])
	localInfo := &localInfo{ID: id, Nick: "poker-player"}

	cctx := &clientCtx{
		ID:     localInfo,
		ctx:    ctx,
		c:      pokerClient,
		cancel: cancel,
		log:    pokerClient.BRClient.LogBackend.Logger("pokerclient"),
	}
	cs[handle] = cctx

	// Start a goroutine to handle client closure and errors
	go func() {
		// Wait for context to be cancelled or client to stop
		<-ctx.Done()

		// Clean up the client if it stops running
		cmtx.Lock()
		delete(cs, handle)
		cmtx.Unlock()

		// Notify the system that the client stopped
		notify(NTClientStopped, nil, ctx.Err())
	}()

	cctx.log.Infof("Poker client initialized with ID: %s", localInfo.ID.String())

	return localInfo, nil
}

// createDefaultConfig creates a default configuration file when none exists
func createDefaultConfig(dataDir, serverAddr, grpcCertPath, debugLevel, brRpcUrl, brClientCert, brClientRpcCert, brClientRpcKey, rpcUser, rpcPass string) error {
	// Ensure the data directory exists
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %v", err)
	}

	// Set default values
	if serverAddr == "" {
		serverAddr = "127.0.0.1:50051" // Default server
	}
	if grpcCertPath == "" {
		grpcCertPath = filepath.Join(dataDir, "server.cert")
	}
	if debugLevel == "" {
		debugLevel = "debug"
	}
	if brRpcUrl == "" {
		brRpcUrl = "wss://127.0.0.1:7777/ws"
	}
	if rpcUser == "" {
		rpcUser = "rpcuser"
	}
	if rpcPass == "" {
		rpcPass = "rpcpass"
	}

	// Note: grpcHost and grpcPort are not needed for the INI format
	// The Flutter config loader will parse the serverAddr directly

	// Create the configuration file content in the correct INI format
	configPath := filepath.Join(dataDir, "pokerui.conf")
	content := fmt.Sprintf(`[default]
serveraddr=%s
datadir=%s
grpcservercert=%s
address=
brrpcurl=%s
brclientcert=%s
brclientrpccert=%s
brclientrpckey=%s
rpcuser=%s
rpcpass=%s

[clientrpc]
wantsLogNtfns=0

[log]
debuglevel=%s
maxlogfiles=5
maxbufferlines=1000
`,
		serverAddr, dataDir, grpcCertPath, brRpcUrl, brClientCert, brClientRpcCert, brClientRpcKey, rpcUser, rpcPass, debugLevel)

	// Write the configuration file
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write config file: %v", err)
	}

	// Create default server certificate if it doesn't exist
	if _, err := os.Stat(grpcCertPath); os.IsNotExist(err) {
		if err := createDefaultServerCert(grpcCertPath); err != nil {
			return fmt.Errorf("failed to create default server certificate: %v", err)
		}
	}

	return nil
}

// createDefaultServerCert creates a default server certificate file
func createDefaultServerCert(certPath string) error {
	// Ensure the directory exists
	dir := filepath.Dir(certPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create cert directory: %v", err)
	}

	// Default server certificate content
	defaultCert := `-----BEGIN CERTIFICATE-----
MIIBzDCCAXGgAwIBAgIRAKzgtkERbGLTLSM3kvtKq4YwCgYIKoZIzj0EAwIwKzER
MA8GA1UEChMIZ2VuY2VydHMxFjAUBgNVBAMTDTE5Mi4xNjguMC4xMDkwHhcNMjUw
NTIxMTcwMzEyWhcNMzUwNTIwMTcwMzEyWjArMREwDwYDVQQKEwhnZW5jZXJ0czEW
MBQGA1UEAxMNMTkyLjE2OC4wLjEwOTBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IA
BCeYEkUALzxW+deCYqEXk9n5SXpm/0k7cprUzOhyxo3rgFEcXAswmtuTj4aRItsV
mHWffXRqnTRQmPMjlngoHBijdjB0MA4GA1UdDwEB/wQEAwIChDAPBgNVHRMBAf8E
BTADAQH/MB0GA1UdDgQWBBQVCe1KJ5IC9UbKr0CxQ8zoc/DcQTAyBgNVHREEKzAp
gglsb2NhbGhvc3SHBMCoAG2HBH8AAAGHEAAAAAAAAAAAAAAAAAAAAAEwCgYIKoZI
zj0EAwIDSQAwRgIhAK2zFZM5R6hjDnSVDZFqgL7Glnc1kYm0WwAyuqQ3u6pSAiEA
stnyeJa1nliPo5mCKwgl5c2S/knBIm6f0y61CN6IFWw=
-----END CERTIFICATE-----`

	// Write the certificate file
	if err := os.WriteFile(certPath, []byte(defaultCert), 0644); err != nil {
		return fmt.Errorf("failed to write cert file: %v", err)
	}

	return nil
}

// handleCreateDefaultConfig handles the CTCreateDefaultConfig command
func handleCreateDefaultConfig(args createDefaultConfigArgs) (map[string]string, error) {
	if err := createDefaultConfig(args.DataDir, args.ServerAddr, args.GRPCCertPath, args.DebugLevel,
		args.BrRpcUrl, args.BrClientCert, args.BrClientRpcCert, args.BrClientRpcKey, args.RpcUser, args.RpcPass); err != nil {
		return nil, err
	}

	return map[string]string{
		"status":      "created",
		"config_path": filepath.Join(args.DataDir, "pokerui.conf"),
	}, nil
}

// handleCreateDefaultServerCert handles the CTCreateDefaultServerCert command
func handleCreateDefaultServerCert(certPath string) (map[string]string, error) {
	if err := createDefaultServerCert(certPath); err != nil {
		return nil, err
	}

	return map[string]string{
		"status":    "created",
		"cert_path": certPath,
	}, nil
}

// handleLoadConfig loads config from a provided path (either a file path to
// pokerui.conf or a datadir) and returns a flat map for Flutter.
func handleLoadConfig(pathOrDir string) (map[string]interface{}, error) {
	datadir := pathOrDir
	if datadir == "" {
		return nil, fmt.Errorf("empty path")
	}
	// If a file path was provided, use its directory as datadir.
	if strings.HasSuffix(strings.ToLower(datadir), ".conf") {
		datadir = filepath.Dir(datadir)
	}

	cfg, err := client.LoadAppConfig(datadir, client.ConfigOverrides{})
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	serverAddr := ""
	if cfg.GRPCHost != "" && cfg.GRPCPort != "" {
		serverAddr = fmt.Sprintf("%s:%s", cfg.GRPCHost, cfg.GRPCPort)
	}

	// Build a map compatible with Flutter Config expectations
	res := map[string]interface{}{
		"server_addr":          serverAddr,
		"grpc_cert_path":       cfg.GRPCServerCert,
		"rpc_websocket_url":    cfg.BRConfig.RPCURL,
		"rpc_cert_path":        cfg.BRConfig.BRClientCert,
		"rpc_client_cert_path": cfg.BRConfig.BRClientRPCCert,
		"rpc_client_key_path":  cfg.BRConfig.BRClientRPCKey,
		"rpc_user":             cfg.BRConfig.RPCUser,
		"rpc_pass":             cfg.BRConfig.RPCPass,
		"debug_level":          cfg.BRConfig.Debug,
		"wants_log_ntfns":      false,
		"datadir":              cfg.DataDir,
		"payout_address":       cfg.PayoutAddress,
	}

	return res, nil
}

// handleInitPokerClient initializes a new poker client
func handleInitPokerClient(handle uint32, args initPokerClient) (*localInfo, error) {
	// Ensure the data directory exists first
	if err := os.MkdirAll(args.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory %s: %v", args.DataDir, err)
	}

	// Ensure the logs subdirectory exists
	logsDir := filepath.Dir(args.LogFile)
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create logs directory %s: %v", logsDir, err)
	}

	logBackend, err := logging.NewLogBackend(logging.LogConfig{
		LogFile:        args.LogFile,
		DebugLevel:     args.DebugLevel,
		MaxLogFiles:    10,
		MaxBufferLines: 1000,
	})
	if err != nil {
		return nil, err
	}
	log := logBackend.Logger("pokerui")

	// Initialize clientID using a random 32-byte value
	var rnd [32]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, fmt.Errorf("failed to generate random id: %v", err)
	}
	var id zkidentity.ShortID
	id.FromBytes(rnd[:])
	localInfo := &localInfo{ID: id, Nick: "poker-player"}

	// Load application config from datadir with gRPC overrides only.
	// BR-related settings are loaded from file; Flutter should not handle them.
	cfg, err := client.LoadAppConfig(args.DataDir, client.ConfigOverrides{
		GRPCHost:       args.GRPCHost,
		GRPCPort:       args.GRPCPort,
		GRPCServerCert: args.GRPCServerCert,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %v", err)
	}

	// Apply runtime flag-like overrides
	flagOverrides := map[string]interface{}{
		"grpcinsecure": args.Insecure,
		"offline":      args.Offline,
	}
	if args.PlayerID != "" {
		flagOverrides["playerid"] = args.PlayerID
	}
	if args.LogFile != "" {
		flagOverrides["logfile"] = args.LogFile
	}
	if args.DebugLevel != "" {
		flagOverrides["debug"] = args.DebugLevel
	}
	if args.GRPCHost != "" {
		flagOverrides["grpchost"] = args.GRPCHost
	}
	if args.GRPCPort != "" {
		flagOverrides["grpcport"] = args.GRPCPort
	}
	if args.GRPCServerCert != "" {
		flagOverrides["grpcservercert"] = args.GRPCServerCert
	}
	cfg.SetConfigValues(flagOverrides)

	// Validate configuration before starting
	if err := cfg.ValidateConfig(); err != nil {
		return nil, fmt.Errorf("configuration validation error: %v", err)
	}

	// Initialize notification manager (required by NewPokerClient)
	cfg.Notifications = client.NewNotificationManager()

	// Create a context used for the client lifecycle
	ctx, cancel := context.WithCancel(context.Background())

	// Create the poker client
	pc, err := client.NewPokerClient(ctx, cfg)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create poker client: %v", err)
	}

	// Start the notification stream
	if err := pc.StartNotificationStream(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start notifications: %v", err)
	}

	cctx := &clientCtx{
		ID:     localInfo,
		ctx:    ctx,
		c:      pc,
		cancel: cancel,
		log:    log,
	}

	// Store in global map
	cmtx.Lock()
	if cs == nil {
		cs = make(map[uint32]*clientCtx)
	}
	cs[handle] = cctx
	cmtx.Unlock()

	log.Infof("Poker client initialized with ID: %s", localInfo.ID.String())

	return localInfo, nil
}

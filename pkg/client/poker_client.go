package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/decred/slog"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/companyzero/bisonrelay/clientrpc/types"
	"github.com/vctt94/bisonbotkit/botclient"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
	pokerutils "github.com/vctt94/poker-bisonrelay/pkg/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Message types for UI communication
type GameUpdateMsg *pokerrpc.GameUpdate

// TableCreateConfig holds configuration for creating a new table
type TableCreateConfig struct {
	SmallBlind    int64
	BigBlind      int64
	MinPlayers    int32
	MaxPlayers    int32
	BuyIn         int64
	MinBalance    int64
	StartingChips int64
}

// PokerClient represents a poker client with notification handling
type PokerClient struct {
	sync.RWMutex
	ID           string
	DataDir      string
	BRClient     *botclient.BotClient
	LobbyService pokerrpc.LobbyServiceClient
	PokerService pokerrpc.PokerServiceClient
	conn         *grpc.ClientConn
	IsReady      bool
	BetAmt       int64 // bet amount in atoms
	tableID      string
	cfg          *PokerClientConfig
	ntfns        *NotificationManager
	log          slog.Logger
	logBackend   *logging.LogBackend
	notifier     pokerrpc.LobbyService_StartNotificationStreamClient
	UpdatesCh    chan tea.Msg
	ErrorsCh     chan error

	// Game streaming
	gameStream   pokerrpc.PokerService_StartGameStreamClient
	gameStreamMu sync.Mutex

	// For reconnection handling
	ctx          context.Context
	cancelFunc   context.CancelFunc
	reconnecting bool
	reconnectMu  sync.Mutex
}

// NewPokerClient creates a new poker client with notification support
func NewPokerClient(ctx context.Context, cfg *PokerClientConfig) (*PokerClient, error) {
	// Validate that notifications are properly initialized
	if cfg.Notifications == nil {
		// initialize notification manager with NewNotificationManager
		return nil, fmt.Errorf("notification manager cannot be nil - client startup aborted")
	}

	// Create the base client
	client, err := newClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create base client: %v", err)
	}

	ctx, cancel := context.WithCancel(ctx)

	pc := &PokerClient{
		ID:           client.ID,
		DataDir:      client.DataDir,
		BRClient:     client.BRClient,
		LobbyService: client.LobbyService,
		PokerService: client.PokerService,
		conn:         client.conn,
		cfg:          cfg,
		ntfns:        cfg.Notifications,
		log:          client.log,
		logBackend:   client.logBackend,
		UpdatesCh:    make(chan tea.Msg, 100),
		ErrorsCh:     make(chan error, 10),
		ctx:          ctx,
		cancelFunc:   cancel,
	}

	// Final validation that client is properly initialized
	if err := pc.Validate(); err != nil {
		return nil, fmt.Errorf("client validation failed: %v", err)
	}

	return pc, nil
}

// newBaseClient creates a basic client without notification support (internal use)
func newClient(ctx context.Context, cfg *PokerClientConfig) (*PokerClient, error) {
	// Ensure datadir exists
	if err := pokerutils.EnsureDataDirExists(cfg.DataDir); err != nil {
		return nil, fmt.Errorf("failed to create datadir: %v", err)
	}

	// Convert to BisonRelay config
	clientConfig := cfg.ToBisonRelayConfig()

	// Initialize BisonRelay client
	brClient, err := botclient.NewClient(clientConfig)
	if err != nil {
		fmt.Printf("Failed to create bot client: %v\n", err)
		os.Exit(1)
	}
	log := brClient.LogBackend.Logger("PokerClient")

	client := &PokerClient{
		DataDir:    cfg.DataDir,
		BRClient:   brClient,
		log:        log,
		logBackend: brClient.LogBackend,
		cfg:        cfg,
	}

	// Start the RPC client in a goroutine if brClient was created successfully
	if brClient != nil {
		go brClient.RunRPC(ctx)

		// Get the client ID
		var publicIdentity types.PublicIdentity
		err = brClient.Chat.UserPublicIdentity(ctx, &types.PublicIdentityReq{}, &publicIdentity)
		if err != nil {
			log.Errorf("Failed to get user public identity: %v", err)
		} else {
			// Convert the identity to a hex string for use as client ID
			client.ID = hex.EncodeToString(publicIdentity.Identity[:])
		}
	}

	// Use a fallback client ID if BR client failed or no ID was obtained
	if client.ID == "" {
		client.ID = fmt.Sprintf("client-%d", os.Getpid())
		log.Warnf("Using fallback client ID: %s", client.ID)
	}

	log.Infof("Using client ID: %s", client.ID)

	// Connect to the poker server
	if err := client.connectToPokerServer(ctx, cfg.GRPCHost); err != nil {
		return nil, fmt.Errorf("failed to connect to poker server: %v", err)
	}

	// Initialize account
	if err := client.initializeAccount(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize account: %v", err)
	}

	return client, nil
}

// connectToPokerServer establishes gRPC connection to the poker server
func (pc *PokerClient) connectToPokerServer(ctx context.Context, grpcHost string) error {
	var dialOpts []grpc.DialOption

	if pc.cfg == nil {
		return fmt.Errorf("cfg is nil")
	}

	// Check if GRPCHost and GRPCPort are properly configured
	if pc.cfg.GRPCHost == "" {
		return fmt.Errorf("GRPCHost is not configured")
	}
	if pc.cfg.GRPCPort == "" {
		return fmt.Errorf("GRPCPort is not configured")
	}

	// Use TLS
	grpcServerCertPath := pc.cfg.GRPCServerCert
	if grpcServerCertPath == "" {
		grpcServerCertPath = filepath.Join(pc.DataDir, "server.cert")
	}

	// Check if server certificate exists, create default one if not
	if _, err := os.Stat(grpcServerCertPath); os.IsNotExist(err) {
		if err := CreateDefaultServerCert(grpcServerCertPath); err != nil {
			return fmt.Errorf("failed to create default server certificate: %v", err)
		}
	}

	// Load the server certificate
	pemServerCA, err := os.ReadFile(grpcServerCertPath)
	if err != nil {
		return fmt.Errorf("failed to read server certificate: %v", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(pemServerCA) {
		return fmt.Errorf("failed to add server certificate to pool")
	}

	// Use GRPCHost for TLS ServerName, fallback to grpcHost parameter if needed
	serverName := pc.cfg.GRPCHost
	if serverName == "" {
		serverName = grpcHost
	}
	if serverName == "" {
		serverName = "localhost" // fallback
	}

	// Create the TLS credentials with ServerName
	tlsConfig := &tls.Config{
		RootCAs:    certPool,
		ServerName: serverName,
	}

	creds := credentials.NewTLS(tlsConfig)
	dialOpts = append(dialOpts, grpc.WithTransportCredentials(creds))

	// Construct server address from GRPCHost and GRPCPort
	serverAddr := fmt.Sprintf("%s:%s", pc.cfg.GRPCHost, pc.cfg.GRPCPort)

	// Create the client connection
	conn, err := grpc.Dial(serverAddr, dialOpts...)
	if err != nil {
		return fmt.Errorf("failed to connect: %v", err)
	}

	pc.conn = conn
	pc.LobbyService = pokerrpc.NewLobbyServiceClient(conn)
	pc.PokerService = pokerrpc.NewPokerServiceClient(conn)

	return nil
}

// initializeAccount ensures the client has an account with the server
func (pc *PokerClient) initializeAccount(ctx context.Context) error {
	// Make sure we have an account
	balanceResp, err := pc.LobbyService.GetBalance(ctx, &pokerrpc.GetBalanceRequest{
		PlayerId: pc.ID,
	})
	if err != nil {
		// Initialize account with deposit
		updateResp, err := pc.LobbyService.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    pc.ID,
			Amount:      1000,
			Description: "Initial deposit",
		})
		if err != nil {
			return fmt.Errorf("could not initialize balance: %v", err)
		}
		fmt.Printf("Initialized balance: %d\n", updateResp.NewBalance)
	} else {
		fmt.Printf("Current balance: %d\n", balanceResp.Balance)
	}

	return nil
}

// StartNotifier starts the notification stream to receive server notifications
func (pc *PokerClient) StartNotifier(ctx context.Context) error {
	// Validate that client is properly initialized
	if err := pc.Validate(); err != nil {
		return fmt.Errorf("cannot start notifier: %v", err)
	}

	// Create notification stream
	notificationStream, err := pc.LobbyService.StartNotificationStream(ctx, &pokerrpc.StartNotificationStreamRequest{
		PlayerId: pc.ID,
	})
	if err != nil {
		return fmt.Errorf("error creating notification stream: %w", err)
	}
	pc.notifier = notificationStream

	go func() {
		for {
			select {
			case <-ctx.Done():
				pc.log.Info("notification stream closed")
				return
			default:
				ntfn, err := pc.notifier.Recv()
				if err != nil {
					if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "transport is closing") ||
						strings.Contains(err.Error(), "connection is being forcefully terminated") {

						// Try to reconnect
						reconnectErr := pc.reconnect()
						if reconnectErr != nil {
							pc.ErrorsCh <- fmt.Errorf("failed to reconnect: %v", reconnectErr)
						}
						return // This goroutine ends, but a new one will be started by reconnect()
					}

					pc.ErrorsCh <- fmt.Errorf("notification stream error: %v", err)
					return
				}

				// Check if notification is nil
				if ntfn == nil {
					pc.log.Debug("received nil notification")
					continue
				}

				// Check if notification manager is initialized
				if pc.ntfns == nil {
					pc.log.Error("notification manager is nil, skipping notification handling")
					continue
				}

				// Handle notifications based on NotificationType
				ts := time.Now()
				switch ntfn.Type {
				case pokerrpc.NotificationType_TABLE_CREATED:
					if ntfn.Table != nil {
						pc.ntfns.notifyTableCreated(ntfn.Table, ts)
					}

				case pokerrpc.NotificationType_PLAYER_JOINED:
					if ntfn.Table != nil {
						pc.ntfns.notifyPlayerJoined(ntfn.Table, ntfn.PlayerId, ts)
					}

				case pokerrpc.NotificationType_PLAYER_LEFT:
					if ntfn.Table != nil {
						pc.ntfns.notifyPlayerLeft(ntfn.Table, ntfn.PlayerId, ts)
					}

				case pokerrpc.NotificationType_GAME_STARTED:
					if ntfn.Started {
						pc.ntfns.notifyGameStarted(ntfn.TableId, ts)
					}

				case pokerrpc.NotificationType_GAME_ENDED:
					pc.ntfns.notifyGameEnded(ntfn.TableId, ntfn.Message, ts)
					pc.log.Info(ntfn.Message)

				case pokerrpc.NotificationType_BET_MADE:
					pc.ntfns.notifyBetMade(ntfn.PlayerId, ntfn.Amount, ts)
					if ntfn.PlayerId == pc.ID {
						pc.Lock()
						pc.BetAmt = ntfn.Amount
						pc.Unlock()
					}

					// Check the message content to determine specific action type
					if strings.Contains(ntfn.Message, "called") {
						pc.ntfns.notifyPlayerCalled(ntfn.PlayerId, ntfn.Amount, ts)
					} else if strings.Contains(ntfn.Message, "raised") {
						pc.ntfns.notifyPlayerRaised(ntfn.PlayerId, ntfn.Amount, ts)
					} else if strings.Contains(ntfn.Message, "checked") {
						pc.ntfns.notifyPlayerChecked(ntfn.PlayerId, ts)
					}

				case pokerrpc.NotificationType_PLAYER_FOLDED:
					pc.ntfns.notifyPlayerFolded(ntfn.PlayerId, ts)

				case pokerrpc.NotificationType_PLAYER_READY:
					pc.ntfns.notifyPlayerReady(ntfn.PlayerId, ntfn.Ready, ts)
					if ntfn.PlayerId == pc.ID {
						pc.Lock()
						pc.IsReady = ntfn.Ready
						pc.Unlock()
					}
					// Forward notification to UI for any player ready event
					pc.UpdatesCh <- ntfn

				case pokerrpc.NotificationType_PLAYER_UNREADY:
					pc.ntfns.notifyPlayerReady(ntfn.PlayerId, false, ts)
					if ntfn.PlayerId == pc.ID {
						pc.Lock()
						pc.IsReady = false
						pc.Unlock()
					}
					// Forward notification to UI
					pc.UpdatesCh <- ntfn

				case pokerrpc.NotificationType_ALL_PLAYERS_READY:
					// Forward game ready to play notifications to UI
					pc.UpdatesCh <- ntfn

				case pokerrpc.NotificationType_BALANCE_UPDATED:
					pc.ntfns.notifyBalanceUpdated(ntfn.PlayerId, ntfn.NewBalance, ts)

				case pokerrpc.NotificationType_TIP_RECEIVED:
					// Extract tip details from notification
					fromID := ntfn.PlayerId // Assuming the sender is in PlayerId field
					toID := pc.ID           // For now, assume tip is to this client
					amount := ntfn.Amount
					message := ntfn.Message
					pc.ntfns.notifyTipReceived(fromID, toID, amount, message, ts)

				case pokerrpc.NotificationType_SHOWDOWN_RESULT:
					pc.ntfns.notifyShowdownResult(ntfn.TableId, ntfn.Winners, ts)

				case pokerrpc.NotificationType_NEW_ROUND:
					// Forward to UI
					pc.UpdatesCh <- ntfn

				case pokerrpc.NotificationType_SMALL_BLIND_POSTED:
					if pc.ntfns != nil {
						pc.ntfns.notifyBetMade(ntfn.PlayerId, ntfn.Amount, ts)
					}
					pc.log.Infof("Small blind posted: %d chips by %s", ntfn.Amount, ntfn.PlayerId)

				case pokerrpc.NotificationType_BIG_BLIND_POSTED:
					if pc.ntfns != nil {
						pc.ntfns.notifyBetMade(ntfn.PlayerId, ntfn.Amount, ts)
					}
					pc.log.Infof("Big blind posted: %d chips by %s", ntfn.Amount, ntfn.PlayerId)

				default:
					pc.log.Debug("received unknown notification type", "type", ntfn.Type)
				}

				// Always forward raw notification to updates channel for UI handling
				pc.UpdatesCh <- ntfn
			}
		}
	}()

	return nil
}

// reconnect attempts to reconnect to the server and restart the notification stream
func (pc *PokerClient) reconnect() error {
	pc.reconnectMu.Lock()
	defer pc.reconnectMu.Unlock()

	if pc.reconnecting {
		return nil // Already reconnecting
	}

	pc.reconnecting = true
	defer func() { pc.reconnecting = false }()

	pc.log.Info("attempting to reconnect...")

	// Close existing connection
	if pc.conn != nil {
		pc.conn.Close()
	}

	// Create new context for reconnection
	ctx, cancel := context.WithCancel(pc.ctx)
	pc.cancelFunc = cancel

	client, err := newClient(ctx, pc.cfg)
	if err != nil {
		return fmt.Errorf("failed to reconnect client: %v", err)
	}

	// Update client fields
	pc.LobbyService = client.LobbyService
	pc.PokerService = client.PokerService
	pc.conn = client.conn

	// Restart notification stream
	if err := pc.StartNotifier(ctx); err != nil {
		return fmt.Errorf("failed to restart notification stream: %v", err)
	}

	pc.log.Info("successfully reconnected")
	return nil
}

// GetBalance returns the current balance for the player
func (pc *PokerClient) GetBalance(ctx context.Context) (int64, error) {
	resp, err := pc.LobbyService.GetBalance(ctx, &pokerrpc.GetBalanceRequest{
		PlayerId: pc.ID,
	})
	if err != nil {
		return 0, err
	}
	return resp.Balance, nil
}

// JoinTable joins an existing poker table and tracks the table ID
func (pc *PokerClient) JoinTable(ctx context.Context, tableID string) error {
	resp, err := pc.LobbyService.JoinTable(ctx, &pokerrpc.JoinTableRequest{
		PlayerId: pc.ID,
		TableId:  tableID,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to join table: %s", resp.Message)
	}

	pc.Lock()
	pc.tableID = tableID
	pc.Unlock()

	// Start game stream for real-time updates
	if err := pc.StartGameStream(ctx); err != nil {
		pc.log.Warnf("Failed to start game stream: %v", err)
		// Don't return error here since joining was successful
	}

	return nil
}

// CreateTable creates a new poker table and tracks the table ID
func (pc *PokerClient) createTable(ctx context.Context,
	smallBlind, bigBlind int64, maxPlayers, minPlayers int32, minBalance, buyIn, startingChips int64,
) (string, error) {
	resp, err := pc.LobbyService.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:      pc.ID,
		SmallBlind:    smallBlind,
		BigBlind:      bigBlind,
		MaxPlayers:    maxPlayers,
		MinPlayers:    minPlayers,
		MinBalance:    minBalance,
		BuyIn:         buyIn,
		StartingChips: startingChips,
	})
	if err != nil {
		return "", err
	}

	pc.Lock()
	pc.tableID = resp.TableId
	pc.Unlock()

	return resp.TableId, nil
}

// CreateTable creates a new poker table using a configuration struct
func (pc *PokerClient) CreateTable(ctx context.Context, config TableCreateConfig) (string, error) {
	tableID, err := pc.createTable(ctx, config.SmallBlind, config.BigBlind, config.MaxPlayers, config.MinPlayers, config.MinBalance, config.BuyIn, config.StartingChips)
	if err != nil {
		return "", err
	}

	// Start game stream for real-time updates
	if err := pc.StartGameStream(ctx); err != nil {
		pc.log.Warnf("Failed to start game stream: %v", err)
		// Don't return error here since table creation was successful
	}

	return tableID, nil
}

// LeaveTable leaves the current table and clears the table ID
func (pc *PokerClient) LeaveTable(ctx context.Context) error {
	pc.RLock()
	tableID := pc.tableID
	pc.RUnlock()

	if tableID == "" {
		return fmt.Errorf("not currently in a table")
	}

	// Stop game stream first
	pc.stopGameStream()

	resp, err := pc.LobbyService.LeaveTable(ctx, &pokerrpc.LeaveTableRequest{
		PlayerId: pc.ID,
		TableId:  tableID,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to leave table: %s", resp.Message)
	}

	pc.Lock()
	pc.tableID = ""
	pc.Unlock()

	return nil
}

// SetPlayerReady sets the player ready status
func (pc *PokerClient) SetPlayerReady(ctx context.Context) error {
	pc.RLock()
	tableID := pc.tableID
	pc.RUnlock()

	if tableID == "" {
		return fmt.Errorf("not currently in a table")
	}

	resp, err := pc.LobbyService.SetPlayerReady(ctx, &pokerrpc.SetPlayerReadyRequest{
		PlayerId: pc.ID,
		TableId:  tableID,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to set ready: %s", resp.Message)
	}

	return nil
}

// SetPlayerUnready sets the player unready status
func (pc *PokerClient) SetPlayerUnready(ctx context.Context) error {
	pc.RLock()
	tableID := pc.tableID
	pc.RUnlock()

	if tableID == "" {
		return fmt.Errorf("not currently in a table")
	}

	resp, err := pc.LobbyService.SetPlayerUnready(ctx, &pokerrpc.SetPlayerUnreadyRequest{
		PlayerId: pc.ID,
		TableId:  tableID,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to set unready: %s", resp.Message)
	}

	return nil
}

// ShowCards notifies other players that this player is showing their cards
func (pc *PokerClient) ShowCards(ctx context.Context) error {
	pc.RLock()
	tableID := pc.tableID
	pc.RUnlock()

	if tableID == "" {
		return fmt.Errorf("not currently in a table")
	}

	resp, err := pc.LobbyService.ShowCards(ctx, &pokerrpc.ShowCardsRequest{
		PlayerId: pc.ID,
		TableId:  tableID,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to show cards: %s", resp.Message)
	}

	return nil
}

// HideCards notifies other players that this player is hiding their cards
func (pc *PokerClient) HideCards(ctx context.Context) error {
	pc.RLock()
	tableID := pc.tableID
	pc.RUnlock()

	if tableID == "" {
		return fmt.Errorf("not currently in a table")
	}

	resp, err := pc.LobbyService.HideCards(ctx, &pokerrpc.HideCardsRequest{
		PlayerId: pc.ID,
		TableId:  tableID,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to hide cards: %s", resp.Message)
	}

	return nil
}

// GetTables returns all available tables
func (pc *PokerClient) GetTables(ctx context.Context) ([]*pokerrpc.Table, error) {
	resp, err := pc.LobbyService.GetTables(ctx, &pokerrpc.GetTablesRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Tables, nil
}

// GetPlayerCurrentTable returns the current table for the player
func (pc *PokerClient) GetPlayerCurrentTable(ctx context.Context) (string, error) {
	resp, err := pc.LobbyService.GetPlayerCurrentTable(ctx, &pokerrpc.GetPlayerCurrentTableRequest{
		PlayerId: pc.ID,
	})
	if err != nil {
		return "", err
	}
	return resp.TableId, nil
}

// UpdateBalance updates the player's balance
func (pc *PokerClient) UpdateBalance(ctx context.Context, amount int64, description string) (int64, error) {
	resp, err := pc.LobbyService.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
		PlayerId:    pc.ID,
		Amount:      amount,
		Description: description,
	})
	if err != nil {
		return 0, err
	}
	return resp.NewBalance, nil
}

// ProcessTip processes a tip from this player to another player
func (pc *PokerClient) ProcessTip(ctx context.Context, toPlayerID string, amount int64, message string) (int64, error) {
	resp, err := pc.LobbyService.ProcessTip(ctx, &pokerrpc.ProcessTipRequest{
		FromPlayerId: pc.ID,
		ToPlayerId:   toPlayerID,
		Amount:       amount,
		Message:      message,
	})
	if err != nil {
		return 0, err
	}

	if !resp.Success {
		return 0, fmt.Errorf("failed to process tip: %s", resp.Message)
	}

	return resp.NewBalance, nil
}

// GetCurrentTableID returns the current table ID
func (pc *PokerClient) GetCurrentTableID() string {
	pc.RLock()
	defer pc.RUnlock()
	return pc.tableID
}

// Action methods for poker gameplay

// Fold folds the current hand
func (pc *PokerClient) Fold(ctx context.Context) error {
	currentTableID := pc.GetCurrentTableID()
	if currentTableID == "" {
		return fmt.Errorf("not at any table")
	}

	_, err := pc.PokerService.Fold(ctx, &pokerrpc.FoldRequest{
		PlayerId: pc.ID,
		TableId:  currentTableID,
	})
	return err
}

// Check checks (bet 0 when no one has bet)
func (pc *PokerClient) Check(ctx context.Context) error {
	currentTableID := pc.GetCurrentTableID()
	if currentTableID == "" {
		return fmt.Errorf("not at any table")
	}

	_, err := pc.PokerService.Check(ctx, &pokerrpc.CheckRequest{
		PlayerId: pc.ID,
		TableId:  currentTableID,
	})
	return err
}

// Call calls the current bet (matches the current bet amount)
func (pc *PokerClient) Call(ctx context.Context, currentBet int64) error {
	currentTableID := pc.GetCurrentTableID()
	if currentTableID == "" {
		return fmt.Errorf("not at any table")
	}

	_, err := pc.PokerService.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: pc.ID,
		TableId:  currentTableID,
		Amount:   currentBet,
	})
	return err
}

// Raise raises the bet to the specified amount
func (pc *PokerClient) Raise(ctx context.Context, amount int64) error {
	currentTableID := pc.GetCurrentTableID()
	if currentTableID == "" {
		return fmt.Errorf("not at any table")
	}

	_, err := pc.PokerService.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: pc.ID,
		TableId:  currentTableID,
		Amount:   amount,
	})
	return err
}

// Bet makes a bet of the specified amount
func (pc *PokerClient) Bet(ctx context.Context, amount int64) error {
	currentTableID := pc.GetCurrentTableID()
	if currentTableID == "" {
		return fmt.Errorf("not at any table")
	}

	_, err := pc.PokerService.MakeBet(ctx, &pokerrpc.MakeBetRequest{
		PlayerId: pc.ID,
		TableId:  currentTableID,
		Amount:   amount,
	})
	return err
}

// Close closes the poker client and its connections
func (pc *PokerClient) Close() error {
	if pc.cancelFunc != nil {
		pc.cancelFunc()
	}

	// Stop game stream if active
	pc.stopGameStream()

	if pc.conn != nil {
		return pc.conn.Close()
	}
	return nil
}

// StartGameStream starts receiving real-time game updates for the current table
func (pc *PokerClient) StartGameStream(ctx context.Context) error {
	pc.gameStreamMu.Lock()
	defer pc.gameStreamMu.Unlock()

	// Don't start if already streaming
	if pc.gameStream != nil {
		return nil
	}

	currentTableID := pc.GetCurrentTableID()
	if currentTableID == "" {
		return fmt.Errorf("not currently at a table")
	}

	// Start the game stream
	stream, err := pc.PokerService.StartGameStream(ctx, &pokerrpc.StartGameStreamRequest{
		PlayerId: pc.ID,
		TableId:  currentTableID,
	})
	if err != nil {
		return fmt.Errorf("failed to start game stream: %w", err)
	}

	pc.gameStream = stream

	// Start goroutine to handle stream updates
	go pc.handleGameStreamUpdates(ctx)

	pc.log.Infof("Started game stream for table %s", currentTableID)
	return nil
}

// stopGameStream stops the current game stream
func (pc *PokerClient) stopGameStream() {
	pc.gameStreamMu.Lock()
	defer pc.gameStreamMu.Unlock()

	if pc.gameStream != nil {
		pc.gameStream.CloseSend()
		pc.gameStream = nil
		pc.log.Info("Stopped game stream")
	}
}

// handleGameStreamUpdates processes incoming game updates from the stream
func (pc *PokerClient) handleGameStreamUpdates(ctx context.Context) {
	defer func() {
		pc.gameStreamMu.Lock()
		pc.gameStream = nil
		pc.gameStreamMu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			pc.gameStreamMu.Lock()
			stream := pc.gameStream
			pc.gameStreamMu.Unlock()

			if stream == nil {
				return
			}

			update, err := stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "transport is closing") ||
					strings.Contains(err.Error(), "connection is being forcefully terminated") {
					pc.log.Info("Game stream closed")
					return
				}

				pc.ErrorsCh <- fmt.Errorf("game stream error: %v", err)
				return
			}

			// Convert to UI message type and send to updates channel
			select {
			case pc.UpdatesCh <- GameUpdateMsg(update):
			case <-ctx.Done():
				return
			default:
				// Channel is full, drop the update
				pc.log.Warn("Updates channel full, dropping game update")
			}
		}
	}
}

// Validate checks if the PokerClient is properly initialized and ready to use
func (pc *PokerClient) Validate() error {
	if pc == nil {
		return fmt.Errorf("poker client is nil")
	}
	if pc.log == nil {
		return fmt.Errorf("logger is not initialized")
	}
	if pc.logBackend == nil {
		return fmt.Errorf("log backend is not initialized")
	}
	if pc.ntfns == nil {
		return fmt.Errorf("notification manager is not initialized")
	}
	if pc.LobbyService == nil {
		return fmt.Errorf("lobby service is not initialized")
	}
	if pc.PokerService == nil {
		return fmt.Errorf("poker service is not initialized")
	}
	if pc.ID == "" {
		return fmt.Errorf("client ID is not set")
	}
	if pc.UpdatesCh == nil {
		return fmt.Errorf("updates channel is not initialized")
	}
	if pc.ErrorsCh == nil {
		return fmt.Errorf("errors channel is not initialized")
	}
	return nil
}

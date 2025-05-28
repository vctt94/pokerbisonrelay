package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/companyzero/bisonrelay/clientrpc/types"
	"github.com/decred/slog"
	"github.com/vctt94/bisonbotkit/botclient"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/poker-bisonrelay/pkg/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Client represents a poker client with all necessary components
type Client struct {
	ID     string
	Config *PokerClientConfig

	BRClient      *botclient.BotClient
	log           slog.Logger
	LogBackend    *logging.LogBackend
	LobbyService  pokerrpc.LobbyServiceClient
	PokerServcice pokerrpc.PokerServiceClient
	conn          *grpc.ClientConn
}

// NewClient creates a new poker client with the given configuration
func NewClient(ctx context.Context, cfg *PokerClientConfig) (*Client, error) {
	// Ensure datadir exists
	if err := utils.EnsureDataDirExists(cfg.DataDir); err != nil {
		return nil, fmt.Errorf("failed to create datadir: %v", err)
	}

	// Create PokerClientConfig and load configuration
	pokerCfg := &PokerClientConfig{
		DataDir: cfg.DataDir,
	}

	// Load existing config file
	if err := pokerCfg.LoadConfig("pokerclient", cfg.DataDir); err != nil {
		return nil, fmt.Errorf("failed to load configuration: %v", err)
	}

	// Apply overrides from cfg parameter
	if cfg.BRConfig.RPCURL != "" {
		pokerCfg.BRConfig.RPCURL = cfg.BRConfig.RPCURL
	}
	if cfg.GRPCServerCert != "" {
		pokerCfg.GRPCServerCert = cfg.GRPCServerCert
	}
	if cfg.BRConfig.BRClientCert != "" {
		pokerCfg.BRConfig.BRClientCert = cfg.BRConfig.BRClientCert
	}
	if cfg.BRConfig.BRClientRPCCert != "" {
		pokerCfg.BRConfig.BRClientRPCCert = cfg.BRConfig.BRClientRPCCert
	}
	if cfg.BRConfig.BRClientRPCKey != "" {
		pokerCfg.BRConfig.BRClientRPCKey = cfg.BRConfig.BRClientRPCKey
	}
	if cfg.BRConfig.RPCUser != "" {
		pokerCfg.BRConfig.RPCUser = cfg.BRConfig.RPCUser
	}
	if cfg.BRConfig.RPCPass != "" {
		pokerCfg.BRConfig.RPCPass = cfg.BRConfig.RPCPass
	}
	if cfg.GRPCHost != "" {
		pokerCfg.GRPCHost = cfg.GRPCHost
	}
	if cfg.GRPCPort != "" {
		pokerCfg.GRPCPort = cfg.GRPCPort
	}

	// Convert to BisonRelay config (includes grpchost/grpcport in ExtraConfig)
	brConfig := pokerCfg.ToBisonRelayConfig()

	// Initialize BisonRelay client
	brClient, err := botclient.NewClient(brConfig)
	if err != nil {
		fmt.Errorf("Failed to create bot client: %v", err)
		os.Exit(1)
	}
	log := brClient.LogBackend.Logger("PokerClient")

	client := &Client{
		Config: &PokerClientConfig{
			BRConfig:       brConfig,
			DataDir:        cfg.DataDir,
			GRPCHost:       cfg.GRPCHost,
			GRPCPort:       cfg.GRPCPort,
			GRPCServerCert: cfg.GRPCServerCert,
			Notifications:  cfg.Notifications,
		},
		BRClient: brClient,
		log:      log,
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
func (c *Client) connectToPokerServer(ctx context.Context, grpcHost string) error {
	var dialOpts []grpc.DialOption

	// Use TLS
	grpcServerCertPath := c.Config.GRPCServerCert
	if grpcServerCertPath == "" {
		grpcServerCertPath = filepath.Join(c.Config.DataDir, "server.cert")
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

	// Create the TLS credentials with ServerName set to grpcHost
	tlsConfig := &tls.Config{
		RootCAs:    certPool,
		ServerName: grpcHost,
	}

	creds := credentials.NewTLS(tlsConfig)
	dialOpts = append(dialOpts, grpc.WithTransportCredentials(creds))

	grpcHost = c.Config.GRPCHost
	grpcPort := c.Config.GRPCPort
	serverAddr := fmt.Sprintf("%s:%s", grpcHost, grpcPort)
	// Create the client connection
	conn, err := grpc.Dial(serverAddr, dialOpts...)
	if err != nil {
		return fmt.Errorf("failed to connect: %v", err)
	}

	c.conn = conn
	c.LobbyService = pokerrpc.NewLobbyServiceClient(conn)
	c.PokerServcice = pokerrpc.NewPokerServiceClient(conn)

	return nil
}

// initializeAccount ensures the client has an account with the server
func (c *Client) initializeAccount(ctx context.Context) error {
	// Make sure we have an account
	balanceResp, err := c.LobbyService.GetBalance(ctx, &pokerrpc.GetBalanceRequest{
		PlayerId: c.ID,
	})
	if err != nil {
		// Initialize account with deposit
		updateResp, err := c.LobbyService.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    c.ID,
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

// Close closes the client connection
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

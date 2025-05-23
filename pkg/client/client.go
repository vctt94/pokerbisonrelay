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
	"github.com/vctt94/bisonbotkit/botclient"
	"github.com/vctt94/bisonbotkit/config"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/poker-bisonrelay/pkg/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Client represents a poker client with all necessary components
type Client struct {
	ID          string
	Config      *config.ClientConfig
	DataDir     string
	BRClient    interface{} // Use interface{} to avoid type issues
	Logger      interface{} // Use interface{} to avoid type issues
	LobbyClient pokerrpc.LobbyServiceClient
	PokerClient pokerrpc.PokerServiceClient
	conn        *grpc.ClientConn
}

// Config holds the client configuration options
type Config struct {
	ServerAddr         string
	DataDir            string
	RPCURL             string
	GRPCServerCertPath string
	ClientCertPath     string
	ClientKeyPath      string
	RPCUser            string
	RPCPass            string
	GRPCHost           string
	GRPCPort           string
}

// NewClient creates a new poker client with the given configuration
func NewClient(ctx context.Context, cfg *Config, logBackend interface{}) (*Client, error) {
	// Ensure datadir exists
	if err := utils.EnsureDataDirExists(cfg.DataDir); err != nil {
		return nil, fmt.Errorf("failed to create datadir: %v", err)
	}

	// Load the configuration
	clientConfig, err := config.LoadClientConfig(cfg.DataDir, "pokerclient.conf")
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %v", err)
	}

	// Apply overrides from config
	if cfg.ServerAddr != "" {
		clientConfig.ServerAddr = cfg.ServerAddr
	}
	if cfg.RPCURL != "" {
		clientConfig.RPCURL = cfg.RPCURL
	}
	if cfg.GRPCServerCertPath != "" {
		clientConfig.ServerCertPath = cfg.GRPCServerCertPath
	}
	if cfg.ClientCertPath != "" {
		clientConfig.ClientCertPath = cfg.ClientCertPath
	}
	if cfg.ClientKeyPath != "" {
		clientConfig.ClientKeyPath = cfg.ClientKeyPath
	}
	if cfg.RPCUser != "" {
		clientConfig.RPCUser = cfg.RPCUser
	}
	if cfg.RPCPass != "" {
		clientConfig.RPCPass = cfg.RPCPass
	}

	// Construct server address from host and port if provided
	if cfg.GRPCHost != "" && cfg.GRPCPort != "" {
		clientConfig.ServerAddr = fmt.Sprintf("%s:%s", cfg.GRPCHost, cfg.GRPCPort)
	}

	// Get logger from logBackend
	logBackendTyped, ok := logBackend.(*logging.LogBackend)
	if !ok {
		return nil, fmt.Errorf("invalid logBackend type")
	}
	log := logBackendTyped.Logger("PokerClient")
	log.Infof("Using server address: %s", clientConfig.ServerAddr)

	// Initialize BisonRelay client
	brClient, err := botclient.NewClient(clientConfig, logBackendTyped)
	if err != nil {
		log.Errorf("Failed to create bot client: %v", err)
		// Continue without BR client for now
	}

	client := &Client{
		Config:   clientConfig,
		DataDir:  cfg.DataDir,
		BRClient: brClient,
		Logger:   log,
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
		grpcServerCertPath = filepath.Join(c.DataDir, "server.cert")
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

	// Create the client connection
	conn, err := grpc.Dial(c.Config.ServerAddr, dialOpts...)
	if err != nil {
		return fmt.Errorf("failed to connect: %v", err)
	}

	c.conn = conn
	c.LobbyClient = pokerrpc.NewLobbyServiceClient(conn)
	c.PokerClient = pokerrpc.NewPokerServiceClient(conn)

	return nil
}

// initializeAccount ensures the client has an account with the server
func (c *Client) initializeAccount(ctx context.Context) error {
	// Make sure we have an account
	balanceResp, err := c.LobbyClient.GetBalance(ctx, &pokerrpc.GetBalanceRequest{
		PlayerId: c.ID,
	})
	if err != nil {
		// Initialize account with deposit
		updateResp, err := c.LobbyClient.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
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

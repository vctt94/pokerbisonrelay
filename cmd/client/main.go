package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/companyzero/bisonrelay/clientrpc/types"
	"github.com/vctt94/bisonbotkit/botclient"
	"github.com/vctt94/bisonbotkit/config"
	"github.com/vctt94/bisonbotkit/logging"
	"github.com/vctt94/bisonbotkit/utils"
	"github.com/vctt94/poker-bisonrelay/rpc/grpc/pokerrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var (
	serverAddr             = flag.String("server", "", "Server address")
	datadir                = flag.String("datadir", "", "Directory to load config file from")
	flagURL                = flag.String("url", "", "URL of the websocket endpoint")
	flagGRPCServerCertPath = flag.String("grpcservercert", "", "Path to server.crt file for TLS")
	flagClientCertPath     = flag.String("clientcert", "", "Path to rpc-client.cert file")
	flagClientKeyPath      = flag.String("clientkey", "", "Path to rpc-client.key file")
	rpcUser                = flag.String("rpcuser", "", "RPC user for basic authentication")
	rpcPass                = flag.String("rpcpass", "", "RPC password for basic authentication")
	grpcHost               = flag.String("grpchost", "localhost", "GRPC server hostname")
	grpcPort               = flag.String("grpcport", "50051", "GRPC server port")
)

// ensureDataDirExists creates the datadir and necessary subdirectories if they don't exist
func ensureDataDirExists(datadir string) error {
	// Create main datadir
	if err := os.MkdirAll(datadir, 0700); err != nil {
		return fmt.Errorf("failed to create datadir %s: %v", datadir, err)
	}

	// Create logs subdirectory
	logsDir := filepath.Join(datadir, "logs")
	if err := os.MkdirAll(logsDir, 0700); err != nil {
		return fmt.Errorf("failed to create logs directory %s: %v", logsDir, err)
	}

	return nil
}

// createDefaultServerCert creates a basic server certificate file for testing
// Note: In production, you should use a proper certificate from your server
func createDefaultServerCert(certPath string) error {
	// This is a placeholder self-signed certificate for development/testing
	// In production, you should get this from your actual server
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

	// Create directory for cert file if it doesn't exist
	dir := filepath.Dir(certPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create cert directory %s: %v", dir, err)
	}

	// Write the certificate file
	if err := os.WriteFile(certPath, []byte(defaultCert), 0600); err != nil {
		return fmt.Errorf("failed to write cert file %s: %v", certPath, err)
	}

	return nil
}

func main() {
	flag.Parse()

	// Set up configuration directory
	if *datadir == "" {
		*datadir = utils.AppDataDir("pokerclient", false)
	}

	// Expand tilde in datadir path
	if filepath.HasPrefix(*datadir, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			fmt.Printf("Error getting home directory: %v\n", err)
			os.Exit(1)
		}
		*datadir = filepath.Join(homeDir, (*datadir)[2:])
	}

	// Ensure datadir exists
	if err := ensureDataDirExists(*datadir); err != nil {
		fmt.Printf("Error creating datadir: %v\n", err)
		os.Exit(1)
	}

	// Load the configuration
	cfg, err := config.LoadClientConfig(*datadir, "pokerclient.conf")
	if err != nil {
		fmt.Printf("Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	// Apply overrides from flags
	if *serverAddr != "" {
		cfg.ServerAddr = *serverAddr
	}
	if *flagURL != "" {
		cfg.RPCURL = *flagURL
	}
	if *flagGRPCServerCertPath != "" {
		cfg.ServerCertPath = *flagGRPCServerCertPath
	}
	if *flagClientCertPath != "" {
		cfg.ClientCertPath = *flagClientCertPath
	}
	if *flagClientKeyPath != "" {
		cfg.ClientKeyPath = *flagClientKeyPath
	}
	if *rpcUser != "" {
		cfg.RPCUser = *rpcUser
	}
	if *rpcPass != "" {
		cfg.RPCPass = *rpcPass
	}

	// Construct server address from host and port if provided
	if *grpcHost != "" && *grpcPort != "" {
		cfg.ServerAddr = fmt.Sprintf("%s:%s", *grpcHost, *grpcPort)
	}

	// Set up logging
	useStdout := true
	logBackend, err := logging.NewLogBackend(logging.LogConfig{
		LogFile:        filepath.Join(*datadir, "logs", "pokerclient.log"),
		DebugLevel:     cfg.Debug,
		MaxLogFiles:    10,
		MaxBufferLines: 1000,
		UseStdout:      &useStdout,
	})

	if err != nil {
		fmt.Printf("Failed to set up logging: %v\n", err)
		os.Exit(1)
	}

	log := logBackend.Logger("PokerClient")

	// Log server address after logger is initialized
	log.Infof("Using server address: %s", cfg.ServerAddr)

	// Initialize BisonRelay client
	brClient, err := botclient.NewClient(cfg, logBackend)
	if err != nil {
		log.Errorf("Failed to create bot client: %v", err)
		os.Exit(1)
	}

	// Start the RPC client in a goroutine only if brClient was created successfully
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var clientID string
	if brClient != nil {
		go brClient.RunRPC(ctx)

		// Get the client ID
		var publicIdentity types.PublicIdentity
		err = brClient.Chat.UserPublicIdentity(ctx, &types.PublicIdentityReq{}, &publicIdentity)
		if err != nil {
			log.Errorf("Failed to get user public identity: %v", err)
		}

		// Convert the identity to a hex string for use as client ID
		clientID = hex.EncodeToString(publicIdentity.Identity[:])
	} else {
		// Use a fallback client ID if BR client failed
		clientID = fmt.Sprintf("client-%d", os.Getpid())
		log.Warnf("Using fallback client ID: %s", clientID)
	}

	log.Infof("Using client ID: %s", clientID)

	// Connect to the poker server
	var dialOpts []grpc.DialOption

	// Use TLS
	grpcServerCertPath := cfg.GRPCServerCert
	if *flagGRPCServerCertPath != "" {
		grpcServerCertPath = *flagGRPCServerCertPath
	}
	if grpcServerCertPath == "" {
		grpcServerCertPath = filepath.Join(*datadir, "server.cert")
	}

	log.Infof("Server cert path: %s", grpcServerCertPath)

	// Check if server certificate exists, create default one if not
	if _, err := os.Stat(grpcServerCertPath); os.IsNotExist(err) {
		log.Warnf("Server certificate not found at %s, creating default certificate", grpcServerCertPath)
		if err := createDefaultServerCert(grpcServerCertPath); err != nil {
			log.Errorf("Failed to create default server certificate: %v", err)
			os.Exit(1)
		}
		log.Infof("Created default server certificate at %s", grpcServerCertPath)
	}

	// Load the server certificate
	pemServerCA, err := os.ReadFile(grpcServerCertPath)
	if err != nil {
		log.Errorf("Failed to read server certificate: %v", err)
		os.Exit(1)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(pemServerCA) {
		log.Errorf("Failed to add server certificate to pool")
		os.Exit(1)
	}

	// Create the TLS credentials with ServerName set to grpcHost
	tlsConfig := &tls.Config{
		RootCAs:    certPool,
		ServerName: *grpcHost,
	}

	creds := credentials.NewTLS(tlsConfig)

	dialOpts = append(dialOpts, grpc.WithTransportCredentials(creds))
	log.Infof("Using TLS connection to the server with hostname: %s", *grpcHost)

	// Create the client connection
	conn, err := grpc.Dial(cfg.ServerAddr, dialOpts...)
	if err != nil {
		log.Errorf("Failed to connect: %v", err)
		return
	}
	defer conn.Close()

	// Create the lobby client
	lobbyClient := pokerrpc.NewLobbyServiceClient(conn)

	// Create the poker client for game actions
	pokerClient := pokerrpc.NewPokerServiceClient(conn)

	// Make sure we have an account
	balanceResp, err := lobbyClient.GetBalance(ctx, &pokerrpc.GetBalanceRequest{
		PlayerId: clientID,
	})
	if err != nil {
		log.Errorf("Could not get balance, initializing account: %v", err)
		// Initialize account with deposit
		updateResp, err := lobbyClient.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
			PlayerId:    clientID,
			Amount:      1000,
			Description: "Initial deposit",
		})
		if err != nil {
			log.Errorf("Could not initialize balance: %v", err)
			return
		}
		log.Infof("Initialized balance: %d", updateResp.NewBalance)
	} else {
		log.Infof("Current balance: %d", balanceResp.Balance)
	}

	// Start the UI
	RunUI(ctx, clientID, lobbyClient, pokerClient)
}

// formatCards is a helper function for displaying cards
func formatCards(cards []*pokerrpc.Card) string {
	if len(cards) == 0 {
		return "None"
	}

	result := ""
	for i, card := range cards {
		if i > 0 {
			result += " "
		}
		result += card.Value + card.Suit
	}

	return result
}

package client

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vctt94/bisonbotkit/config"
	"github.com/vctt94/bisonbotkit/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// ClientFlags holds all client command line flags
type ClientFlags struct {
	ServerAddr      *string
	DataDir         *string
	URL             *string
	GRPCServerCert  *string
	BRClientCert    *string
	BRClientRPCCert *string
	BRClientRPCKey  *string
	RPCUser         *string
	RPCPass         *string
	GRPCHost        *string
	GRPCPort        *string
}

// ClientConfig represents the processed client configuration
type ClientConfig struct {
	Cfg        *config.ClientConfig
	DataDir    string
	ServerAddr string
	GRPCHost   string
}

// RegisterClientFlags registers all client command line flags
func RegisterClientFlags() *ClientFlags {
	return &ClientFlags{
		ServerAddr:      flag.String("server", "", "Server address"),
		DataDir:         flag.String("datadir", "", "Directory to load config file from"),
		URL:             flag.String("url", "", "URL of the websocket endpoint"),
		GRPCServerCert:  flag.String("grpcservercert", "", "Path to server.crt file for TLS"),
		BRClientCert:    flag.String("brclientcert", "", "path to brclient rpc.cert file"),
		BRClientRPCCert: flag.String("brclientrpc.cert", "", "Path to rpc-client.cert file"),
		BRClientRPCKey:  flag.String("brclientrpc.key", "", "Path to rpc-client.key file"),
		RPCUser:         flag.String("rpcuser", "", "RPC user for basic authentication"),
		RPCPass:         flag.String("rpcpass", "", "RPC password for basic authentication"),
		GRPCHost:        flag.String("grpchost", "", "GRPC server hostname"),
		GRPCPort:        flag.String("grpcport", "", "GRPC server port"),
	}
}

// LoadClientConfig loads and processes the client configuration
func LoadClientConfig(flags *ClientFlags, appName string) (*ClientConfig, error) {
	// Set up configuration directory
	datadir := *flags.DataDir
	if datadir == "" {
		datadir = utils.AppDataDir(appName, false)
	}

	// Load the configuration
	cfg, err := config.LoadClientConfig(datadir, appName+".conf")
	if err != nil {
		return nil, fmt.Errorf("error loading configuration: %v", err)
	}

	// Apply overrides from flags
	if *flags.ServerAddr != "" {
		cfg.ServerAddr = *flags.ServerAddr
	}
	if *flags.URL != "" {
		cfg.RPCURL = *flags.URL
	}
	if *flags.GRPCServerCert != "" {
		cfg.GRPCServerCert = *flags.GRPCServerCert
	}
	if *flags.BRClientCert != "" {
		cfg.BRClientCert = *flags.BRClientCert
	}
	if *flags.BRClientRPCCert != "" {
		cfg.BRClientRPCCert = *flags.BRClientRPCCert
	}
	if *flags.BRClientRPCKey != "" {
		cfg.BRClientRPCKey = *flags.BRClientRPCKey
	}
	if *flags.RPCUser != "" {
		cfg.RPCUser = *flags.RPCUser
	}
	if *flags.RPCPass != "" {
		cfg.RPCPass = *flags.RPCPass
	}

	// Construct server address from host and port if provided
	serverAddr := cfg.ServerAddr
	if *flags.GRPCHost != "" && *flags.GRPCPort != "" {
		serverAddr = fmt.Sprintf("%s:%s", *flags.GRPCHost, *flags.GRPCPort)
		cfg.ServerAddr = serverAddr
	}

	return &ClientConfig{
		Cfg:        cfg,
		DataDir:    datadir,
		ServerAddr: serverAddr,
		GRPCHost:   *flags.GRPCHost,
	}, nil
}

// CreateDefaultServerCert creates a basic server certificate file for testing
// Note: In production, you should use a proper certificate from your server
func CreateDefaultServerCert(certPath string) error {
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

// SetupGRPCConnection sets up a GRPC connection with TLS credentials
func SetupGRPCConnection(serverAddr, certPath, grpcHost string) (*grpc.ClientConn, error) {
	// Load the server certificate
	pemServerCA, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read server certificate: %v", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(pemServerCA) {
		return nil, fmt.Errorf("failed to add server certificate to pool")
	}

	// Create the TLS credentials with ServerName set to grpcHost
	tlsConfig := &tls.Config{
		RootCAs:    certPool,
		ServerName: grpcHost,
	}

	creds := credentials.NewTLS(tlsConfig)
	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}

	// Create the client connection
	conn, err := grpc.Dial(serverAddr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %v", err)
	}

	return conn, nil
}

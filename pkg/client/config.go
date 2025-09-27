package client

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vctt94/bisonbotkit/config"
	"github.com/vctt94/bisonbotkit/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// PokerClientConfig is the unified configuration structure that handles all configuration concerns
type PokerClientConfig struct {
	// BRConfig holds the brclient configuration options
	BRConfig *config.ClientConfig

	// Data directory
	DataDir string

	// Explicit player ID (used in offline/testing mode)
	PlayerID string

	// gRPC server configuration
	GRPCHost       string
	GRPCPort       string
	GRPCServerCert string

	// Notifications
	Notifications *NotificationManager

	// Test/dev toggles
	Insecure bool // use insecure gRPC (no TLS)
	Offline  bool // do not initialize/connect to BisonRelay
}

// LoadConfig loads and processes the complete configuration from files only
func (cfg *PokerClientConfig) LoadConfig(appName string, datadir string) error {
	// Set up configuration directory
	if datadir == "" {
		datadir = utils.AppDataDir(appName, false)
	}
	cfg.DataDir = datadir

	// Ensure the log directory exists
	logDir := filepath.Join(cfg.DataDir, "logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return fmt.Errorf("failed to create log directory: %v", err)
	}

	// Load existing configuration file if it exists.
	existingConfig, err := config.LoadClientConfig(datadir, appName+".conf")
	if err != nil {
		// Proceed with an empty config; flags may override
		existingConfig = &config.ClientConfig{ExtraConfig: make(map[string]string)}
	}

	if cfg.BRConfig == nil {
		cfg.BRConfig = &config.ClientConfig{}
	}

	// Use existing config as base
	cfg.BRConfig.RPCURL = existingConfig.RPCURL
	cfg.BRConfig.BRClientCert = existingConfig.BRClientCert
	cfg.BRConfig.BRClientRPCCert = existingConfig.BRClientRPCCert
	cfg.BRConfig.BRClientRPCKey = existingConfig.BRClientRPCKey
	cfg.BRConfig.RPCUser = existingConfig.RPCUser
	cfg.BRConfig.RPCPass = existingConfig.RPCPass
	cfg.BRConfig.Debug = existingConfig.Debug
	cfg.BRConfig.LogFile = existingConfig.LogFile
	cfg.BRConfig.MaxLogFiles = existingConfig.MaxLogFiles
	cfg.BRConfig.MaxBufferLines = existingConfig.MaxBufferLines

	// Also set the standalone fields needed for validation
	if grpcServerCert := existingConfig.GetString("grpcservercert"); grpcServerCert != "" {
		cfg.GRPCServerCert = grpcServerCert
	}

	// Load grpchost and grpcport from ExtraConfig if available
	if grpcHost := existingConfig.GetString("grpchost"); grpcHost != "" {
		cfg.GRPCHost = grpcHost
	}
	if grpcPort := existingConfig.GetString("grpcport"); grpcPort != "" {
		cfg.GRPCPort = grpcPort
	}

	// Also check grpcservercert in ExtraConfig if the direct field is empty
	if grpcServerCert := existingConfig.GetString("grpcservercert"); grpcServerCert != "" {
		cfg.GRPCServerCert = grpcServerCert
	}

	return nil
}

// SetConfigValues allows the main app to override configuration values from flags or other sources
func (cfg *PokerClientConfig) SetConfigValues(values map[string]interface{}) {
	for key, value := range values {
		switch key {
		case "id", "playerid":
			if v, ok := value.(string); ok && v != "" {
				cfg.PlayerID = v
			}
		case "rpcurl":
			if v, ok := value.(string); ok && v != "" {
				cfg.BRConfig.RPCURL = v
			}
		case "grpcservercert":
			if v, ok := value.(string); ok && v != "" {
				cfg.GRPCServerCert = v
			}
		case "brclientcert":
			if v, ok := value.(string); ok && v != "" {
				cfg.BRConfig.BRClientCert = v
			}
		case "brclientrpccert":
			if v, ok := value.(string); ok && v != "" {
				cfg.BRConfig.BRClientRPCCert = v
			}
		case "brclientrpckey":
			if v, ok := value.(string); ok && v != "" {
				cfg.BRConfig.BRClientRPCKey = v
			}
		case "rpcuser":
			if v, ok := value.(string); ok && v != "" {
				cfg.BRConfig.RPCUser = v
			}
		case "rpcpass":
			if v, ok := value.(string); ok && v != "" {
				cfg.BRConfig.RPCPass = v
			}
		case "grpchost":
			if v, ok := value.(string); ok && v != "" {
				cfg.GRPCHost = v
			}
		case "grpcport":
			if v, ok := value.(string); ok && v != "" {
				cfg.GRPCPort = v
			}
		case "logfile":
			if v, ok := value.(string); ok && v != "" {
				cfg.BRConfig.LogFile = v
			}
		case "maxlogfiles":
			if v, ok := value.(int); ok {
				cfg.BRConfig.MaxLogFiles = v
			}
		case "maxbufferlines":
			if v, ok := value.(int); ok {
				cfg.BRConfig.MaxBufferLines = v
			}
		case "debug":
			if v, ok := value.(string); ok && v != "" {
				cfg.BRConfig.Debug = v
			}
		case "grpcinsecure":
			if v, ok := value.(bool); ok {
				cfg.Insecure = v
			}
		case "offline":
			if v, ok := value.(bool); ok {
				cfg.Offline = v
			}
		}
	}
}

// ValidateConfig checks that all required configuration values are present
func (cfg *PokerClientConfig) ValidateConfig() error {
	var missingConfigs []string

	if cfg.GRPCHost == "" {
		missingConfigs = append(missingConfigs, "GRPCHost")
	}
	if cfg.GRPCPort == "" {
		missingConfigs = append(missingConfigs, "GRPCPort")
	}
	if !cfg.Insecure {
		if cfg.GRPCServerCert == "" {
			missingConfigs = append(missingConfigs, "GRPCServerCert")
		}
	}

	if !cfg.Offline {
		if cfg.BRConfig.RPCURL == "" {
			missingConfigs = append(missingConfigs, "RPCURL")
		}
		if cfg.BRConfig.RPCUser == "" {
			missingConfigs = append(missingConfigs, "RPCUser")
		}
		if cfg.BRConfig.RPCPass == "" {
			missingConfigs = append(missingConfigs, "RPCPass")
		}
		if cfg.BRConfig.BRClientCert == "" {
			missingConfigs = append(missingConfigs, "BRClientCert")
		}
		if cfg.BRConfig.BRClientRPCCert == "" {
			missingConfigs = append(missingConfigs, "BRClientRPCCert")
		}
		if cfg.BRConfig.BRClientRPCKey == "" {
			missingConfigs = append(missingConfigs, "BRClientRPCKey")
		}
	}

	if len(missingConfigs) > 0 {
		return fmt.Errorf("missing required configuration values: %v", missingConfigs)
	}

	return nil
}

// ToBisonRelayConfig converts PokerClientConfig to BisonRelay's ClientConfig
func (cfg *PokerClientConfig) ToBisonRelayConfig() *config.ClientConfig {
	brConfig := &config.ClientConfig{
		DataDir:         cfg.DataDir,
		RPCURL:          cfg.BRConfig.RPCURL,
		BRClientCert:    cfg.BRConfig.BRClientCert,
		BRClientRPCCert: cfg.BRConfig.BRClientRPCCert,
		BRClientRPCKey:  cfg.BRConfig.BRClientRPCKey,
		RPCUser:         cfg.BRConfig.RPCUser,
		RPCPass:         cfg.BRConfig.RPCPass,
		Debug:           cfg.BRConfig.Debug,
		LogFile:         cfg.BRConfig.LogFile,
		MaxLogFiles:     cfg.BRConfig.MaxLogFiles,
		MaxBufferLines:  cfg.BRConfig.MaxBufferLines,
		ExtraConfig:     make(map[string]string),
	}

	// Set grpchost and grpcport in ExtraConfig
	if cfg.GRPCHost != "" {
		brConfig.SetString("grpchost", cfg.GRPCHost)
	}
	if cfg.GRPCPort != "" {
		brConfig.SetString("grpcport", cfg.GRPCPort)
	}

	return brConfig
}

// CreateDefaultServerCert creates a basic server certificate file for testing
func (cfg *PokerClientConfig) CreateDefaultServerCert() error {
	return CreateDefaultServerCert(cfg.GRPCServerCert)
}

// SetupGRPCConnection sets up a GRPC connection with TLS credentials
func (cfg *PokerClientConfig) SetupGRPCConnection() (*grpc.ClientConn, error) {
	serverAddr := fmt.Sprintf("%s:%s", cfg.GRPCHost, cfg.GRPCPort)
	return SetupGRPCConnection(serverAddr, cfg.GRPCServerCert, cfg.GRPCHost)
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

package bot

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
	"github.com/vctt94/poker-bisonrelay/pkg/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// SetupGRPCServer sets up and returns a configured GRPC server with TLS
func SetupGRPCServer(datadir, certFile, keyFile, serverAddress string, db server.Database) (*grpc.Server, net.Listener, error) {
	// Determine certificate and key file paths
	grpcCertFile := certFile
	grpcKeyFile := keyFile

	// If paths are still empty, use defaults
	if grpcCertFile == "" {
		grpcCertFile = filepath.Join(datadir, "server.cert")
	}
	if grpcKeyFile == "" {
		grpcKeyFile = filepath.Join(datadir, "server.key")
	}

	// Check if certificate files exist
	if _, err := os.Stat(grpcCertFile); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("certificate file not found: %s", grpcCertFile)
	}
	if _, err := os.Stat(grpcKeyFile); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("key file not found: %s", grpcKeyFile)
	}

	// Load TLS credentials
	creds, err := credentials.NewServerTLSFromFile(grpcCertFile, grpcKeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load TLS credentials: %v", err)
	}

	// Create gRPC server with TLS credentials
	grpcServer := grpc.NewServer(grpc.Creds(creds))

	// Create listener
	grpcLis, err := net.Listen("tcp", serverAddress)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to listen for gRPC poker server: %v", err)
	}

	// Initialize and register the poker server
	pokerServer := server.NewServer(db)
	pokerrpc.RegisterLobbyServiceServer(grpcServer, pokerServer)
	pokerrpc.RegisterPokerServiceServer(grpcServer, pokerServer)

	return grpcServer, grpcLis, nil
}

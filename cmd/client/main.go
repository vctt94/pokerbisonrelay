package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/vctt94/poker-bisonrelay/rpc/grpc/pokerrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// Parse command line flags
	serverAddr := flag.String("server", "localhost:50051", "server address")
	playerID := flag.String("player", "test-player", "player ID")
	flag.Parse()

	// Set up connection to server
	conn, err := grpc.Dial(*serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	// Create lobby service client
	lobbyClient := pokerrpc.NewLobbyServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Test getting balance
	balanceResp, err := lobbyClient.GetBalance(ctx, &pokerrpc.GetBalanceRequest{PlayerId: *playerID})
	if err != nil {
		log.Fatalf("could not get balance: %v", err)
	}
	log.Printf("Balance: %d", balanceResp.Balance)

	// Test updating balance
	updateResp, err := lobbyClient.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
		PlayerId:    *playerID,
		Amount:      1000,
		Description: "Initial deposit",
	})
	if err != nil {
		log.Fatalf("could not update balance: %v", err)
	}
	log.Printf("New balance: %d", updateResp.NewBalance)

	// Test creating a table
	tableResp, err := lobbyClient.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:   *playerID,
		SmallBlind: 10,
		BigBlind:   20,
		MinPlayers: 2,
		MaxPlayers: 6,
	})
	if err != nil {
		log.Fatalf("could not create table: %v", err)
	}
	log.Printf("Created table: %s", tableResp.TableId)

	// Test getting game state
	stateResp, err := lobbyClient.GetTables(ctx, &pokerrpc.GetTablesRequest{})
	if err != nil {
		log.Fatalf("could not get tables: %v", err)
	}
	log.Printf("Tables: %+v", stateResp.Tables)
}

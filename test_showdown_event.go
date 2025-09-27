package main

import (
	"fmt"
	"log"
	"time"

	"github.com/vctt94/pokerbisonrelay/pkg/poker"
)

func main() {
	// Create a simple test to verify showdown event publishing
	fmt.Println("Testing showdown event implementation...")

	// Create a test table
	cfg := poker.TableConfig{
		ID:             "test_table",
		Log:            nil, // Will use default logger
		GameLog:        nil,
		HostID:         "test_host",
		BuyIn:          1000,
		MinPlayers:     2,
		MaxPlayers:     3,
		SmallBlind:     10,
		BigBlind:       20,
		MinBalance:     1000,
		StartingChips:  1000,
		TimeBank:       30 * time.Second,
		AutoStartDelay: 5 * time.Second,
	}

	table := poker.NewTable(cfg)

	// Set up event publisher callback
	eventPublished := false
	table.SetEventPublisher(func(eventType string, tableID string, amount int64, metadata map[string]interface{}) {
		fmt.Printf("Event published: type=%s, tableID=%s, amount=%d\n", eventType, tableID, amount)
		if eventType == "showdown_result" {
			eventPublished = true
			fmt.Printf("Showdown event metadata: %+v\n", metadata)
		}
	})

	// Add test users
	table.AddNewUser("player1", "Player 1", 10000, 0)
	table.AddNewUser("player2", "Player 2", 10000, 1)

	// Set players ready
	table.SetPlayerReady("player1", true)
	table.SetPlayerReady("player2", true)

	// Trigger state machine to check if all players are ready
	table.CheckAllPlayersReady()

	// Start game
	err := table.StartGame()
	if err != nil {
		log.Fatalf("Failed to start game: %v", err)
	}

	// Manually trigger showdown by calling handleShowdown
	// This simulates what happens when the game reaches showdown
	fmt.Println("Manually triggering showdown...")

	// Debug: Check current game state
	game := table.GetGame()
	if game != nil {
		fmt.Printf("Game phase: %v\n", game.GetPhase())
		fmt.Printf("Current player: %s\n", table.GetCurrentPlayerID())
		fmt.Printf("Actions in round: %d\n", game.GetActionsInRound())
		fmt.Printf("Current bet: %d\n", table.GetCurrentBet())
	}

	// Complete all betting rounds to reach showdown
	// PRE_FLOP round
	fmt.Println("=== PRE_FLOP ROUND ===")
	fmt.Println("Player1 calling...")
	err = table.HandleCall("player1")
	if err != nil {
		fmt.Printf("Player1 call failed: %v\n", err)
	}

	fmt.Println("Player2 checking...")
	err = table.HandleCheck("player2")
	if err != nil {
		fmt.Printf("Player2 check failed: %v\n", err)
	}

	// FLOP round
	fmt.Println("=== FLOP ROUND ===")
	fmt.Printf("Before FLOP - Community cards: %d\n", len(game.GetCommunityCards()))
	fmt.Println("Player1 checking...")
	err = table.HandleCheck("player1")
	if err != nil {
		fmt.Printf("Player1 check failed: %v\n", err)
	}

	fmt.Println("Player2 checking...")
	err = table.HandleCheck("player2")
	if err != nil {
		fmt.Printf("Player2 check failed: %v\n", err)
	}
	fmt.Printf("After FLOP - Community cards: %d\n", len(game.GetCommunityCards()))

	// TURN round
	fmt.Println("=== TURN ROUND ===")
	fmt.Println("Player1 checking...")
	err = table.HandleCheck("player1")
	if err != nil {
		fmt.Printf("Player1 check failed: %v\n", err)
	}

	fmt.Println("Player2 checking...")
	err = table.HandleCheck("player2")
	if err != nil {
		fmt.Printf("Player2 check failed: %v\n", err)
	}

	// RIVER round
	fmt.Println("=== RIVER ROUND ===")
	fmt.Println("Player1 checking...")
	err = table.HandleCheck("player1")
	if err != nil {
		fmt.Printf("Player1 check failed: %v\n", err)
	}

	fmt.Println("Player2 checking...")
	err = table.HandleCheck("player2")
	if err != nil {
		fmt.Printf("Player2 check failed: %v\n", err)
	}

	// Debug: Check final state
	if game != nil {
		fmt.Printf("Final game phase: %v\n", game.GetPhase())
		fmt.Printf("Actions in round: %d\n", game.GetActionsInRound())
		fmt.Printf("Community cards: %d\n", len(game.GetCommunityCards()))
		fmt.Printf("Player1 hand: %d cards\n", len(game.GetPlayers()[0].Hand))
		fmt.Printf("Player2 hand: %d cards\n", len(game.GetPlayers()[1].Hand))
	}

	// Check if event was published
	if eventPublished {
		fmt.Println("✅ Showdown event was published successfully!")
	} else {
		fmt.Println("❌ Showdown event was NOT published")
	}
}

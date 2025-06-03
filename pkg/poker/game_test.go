package poker

import (
	"strings"
	"testing"
)

func TestNewGame(t *testing.T) {
	cfg := GameConfig{
		NumPlayers: 2,
		Seed:       42, // Use a fixed seed for deterministic testing
	}

	game := NewGame(cfg)

	if len(game.players) != 2 {
		t.Errorf("Expected 2 players, got %d", len(game.players))
	}

	if game.maxRounds != 1 {
		t.Errorf("Expected maxRounds=1, got %d", game.maxRounds)
	}

	// Check initial player state
	for i, player := range game.players {
		if player.Balance != 100 {
			t.Errorf("Player %d: Expected 100 balance, got %d", i, player.Balance)
		}
		if player.HasFolded {
			t.Errorf("Player %d: Expected not folded", i)
		}
		if player.HasBet != 0 {
			t.Errorf("Player %d: Expected 0 bet, got %d", i, player.HasBet)
		}
	}

	// Check deck is properly initialized
	if game.deck == nil {
		t.Error("Expected deck to be initialized")
	}
	if game.deck.Size() != 52 {
		t.Errorf("Expected deck size 52, got %d", game.deck.Size())
	}
}

func TestNewGamePanicsOnInvalidPlayers(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with < 2 players")
		}
	}()

	cfg := GameConfig{
		NumPlayers: 1,
	}
	NewGame(cfg)
}

func TestDealCards(t *testing.T) {
	cfg := GameConfig{
		NumPlayers: 2,
		Seed:       42,
	}

	game := NewGame(cfg)
	err := game.DealCards()
	if err != nil {
		t.Fatalf("Failed to deal cards: %v", err)
	}

	// Check each player has 2 cards
	for i, player := range game.players {
		if len(player.Hand) != 2 {
			t.Errorf("Player %d: Expected 2 cards, got %d", i, len(player.Hand))
		}
	}

	// Check deck has correct number of cards remaining
	expectedRemaining := 52 - (2 * len(game.players))
	if game.deck.Size() != expectedRemaining {
		t.Errorf("Expected %d cards remaining, got %d", expectedRemaining, game.deck.Size())
	}
}

func TestCommunityCards(t *testing.T) {
	cfg := GameConfig{
		NumPlayers: 2,
		Seed:       42,
	}

	game := NewGame(cfg)
	err := game.DealCards()
	if err != nil {
		t.Fatalf("Failed to deal cards: %v", err)
	}

	// Check initial community cards
	if len(game.communityCards) != 0 {
		t.Errorf("Expected 0 community cards initially, got %d", len(game.communityCards))
	}

	// Deal flop
	game.StateFlop()
	if len(game.communityCards) != 3 {
		t.Errorf("Expected 3 community cards after flop, got %d", len(game.communityCards))
	}

	// Deal turn
	game.StateTurn()
	if len(game.communityCards) != 4 {
		t.Errorf("Expected 4 community cards after turn, got %d", len(game.communityCards))
	}

	// Deal river
	game.StateRiver()
	if len(game.communityCards) != 5 {
		t.Errorf("Expected 5 community cards after river, got %d", len(game.communityCards))
	}
}

func TestShowdown(t *testing.T) {
	// Create a game with 2 players
	cfg := GameConfig{
		NumPlayers: 2,
		Seed:       42,
	}

	game := NewGame(cfg)

	// Set up player hands manually
	player1 := game.players[0]
	player2 := game.players[1]

	// Reset balances to 0 for this test
	player1.Balance = 0
	player2.Balance = 0

	// Player 1 has a pair of Aces
	player1.Hand = []Card{
		{suit: Hearts, value: Ace},
		{suit: Spades, value: Ace},
	}

	// Player 2 has King-Queen
	player2.Hand = []Card{
		{suit: Hearts, value: King},
		{suit: Spades, value: Queen},
	}

	// Set community cards: 2-5-7-9-Jack (no help for either player)
	game.communityCards = []Card{
		{suit: Clubs, value: Two},
		{suit: Diamonds, value: Five},
		{suit: Hearts, value: Seven},
		{suit: Spades, value: Nine},
		{suit: Clubs, value: Jack},
	}

	// Set up pot
	game.potManager = NewPotManager()
	game.potManager.AddBet(0, 50) // Player 1 bet 50
	game.potManager.AddBet(1, 50) // Player 2 bet 50

	// Run the showdown
	stateShowdown(game)

	// Player 1 should win with pair of Aces
	if player1.Balance != 100 {
		t.Errorf("Expected player 1 to win with pot of 100, got %d", player1.Balance)
	}

	// Player 2 should not win anything
	if player2.Balance != 0 {
		t.Errorf("Expected player 2 to not win anything, got %d", player2.Balance)
	}

	// Check hand descriptions
	if player1.HandDescription != "Pair of As" && !strings.Contains(player1.HandDescription, "Pair of A") {
		t.Errorf("Expected pair of Aces description, got %s", player1.HandDescription)
	}

	if player2.HandDescription != "High Card K" && !strings.Contains(player2.HandDescription, "High Card K") {
		t.Errorf("Expected high card King description, got %s", player2.HandDescription)
	}
}

func TestTieBreakerShowdown(t *testing.T) {
	// Create a game with 3 players
	cfg := GameConfig{
		NumPlayers: 3,
		Seed:       42,
	}

	game := NewGame(cfg)

	// Set up player hands manually
	player1 := game.players[0]
	player2 := game.players[1]
	player3 := game.players[2]

	// Reset balances to 0 for this test
	player1.Balance = 0
	player2.Balance = 0
	player3.Balance = 0

	// All players have a pair of Aces but with different kickers
	player1.Hand = []Card{
		{suit: Hearts, value: Ace},
		{suit: Spades, value: Ace},
	}

	player2.Hand = []Card{
		{suit: Clubs, value: Ace},
		{suit: Diamonds, value: Ace},
	}

	player3.Hand = []Card{
		{suit: Hearts, value: King},
		{suit: Spades, value: King}, // Lower pair
	}

	// Set community cards: 2-5-7-9-Jack
	game.communityCards = []Card{
		{suit: Clubs, value: Two},
		{suit: Diamonds, value: Five},
		{suit: Hearts, value: Seven},
		{suit: Spades, value: Nine},
		{suit: Clubs, value: Jack},
	}

	// Mark player 3 as folded
	player3.HasFolded = true

	// Set up pot
	game.potManager = NewPotManager()
	game.potManager.AddBet(0, 50) // Player 1 bet 50
	game.potManager.AddBet(1, 50) // Player 2 bet 50
	// Player 3 folded, no bet

	// Run the showdown
	stateShowdown(game)

	// Players 1 and 2 should tie and split the pot (50 each)
	if player1.Balance != 50 {
		t.Errorf("Expected player 1 to win 50 (half pot), got %d", player1.Balance)
	}

	if player2.Balance != 50 {
		t.Errorf("Expected player 2 to win 50 (half pot), got %d", player2.Balance)
	}

	// Player 3 folded so should not win anything
	if player3.Balance != 0 {
		t.Errorf("Expected player 3 to not win anything, got %d", player3.Balance)
	}
}

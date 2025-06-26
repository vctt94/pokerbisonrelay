package poker

import (
	"strings"
	"testing"
)

func TestNewGame(t *testing.T) {
	cfg := GameConfig{
		NumPlayers:    2,
		StartingChips: 1000, // Set to 1000 to match the expected balance
		Seed:          42,   // Use a fixed seed for deterministic testing
	}

	game := NewGame(cfg)

	// After refactor, game starts with empty players slice
	// Table manages players and calls SetPlayers
	if len(game.players) != 0 {
		t.Errorf("Expected 0 players initially, got %d", len(game.players))
	}

	// Create test users and set them in the game
	users := []*User{
		NewUser("player1", "Player 1", 1000, 0),
		NewUser("player2", "Player 2", 1000, 1),
	}
	game.SetPlayers(users)

	// Now check that players were created correctly
	if len(game.players) != 2 {
		t.Errorf("Expected 2 players after SetPlayers, got %d", len(game.players))
	}

	// Check initial player state
	for i, player := range game.players {
		if player.Balance != 1000 {
			t.Errorf("Player %d: Expected 1000 balance, got %d", i, player.Balance)
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
		NumPlayers:    1,
		StartingChips: 100,
	}
	NewGame(cfg)
}

func TestDealCards(t *testing.T) {
	cfg := GameConfig{
		NumPlayers:    2,
		StartingChips: 100,
		Seed:          42,
	}

	game := NewGame(cfg)

	// Create test users and set them in the game
	users := []*User{
		NewUser("player1", "Player 1", 100, 0),
		NewUser("player2", "Player 2", 100, 1),
	}
	game.SetPlayers(users)

	// Deal cards manually for testing (since DealCards was removed)
	for _, player := range game.players {
		for i := 0; i < 2; i++ {
			card, ok := game.deck.Draw()
			if !ok {
				t.Fatalf("Failed to draw card from deck")
			}
			player.Hand = append(player.Hand, card)
		}
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

	// Create test users and set them in the game
	users := []*User{
		NewUser("player1", "Player 1", 100, 0),
		NewUser("player2", "Player 2", 100, 1),
	}
	game.SetPlayers(users)

	// Deal cards manually for testing (since DealCards was removed)
	for _, player := range game.players {
		for i := 0; i < 2; i++ {
			card, ok := game.deck.Draw()
			if !ok {
				t.Fatalf("Failed to draw card from deck")
			}
			player.Hand = append(player.Hand, card)
		}
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

	// Create test users and set them in the game
	users := []*User{
		NewUser("player1", "Player 1", 0, 0), // Start with 0 balance for clean test
		NewUser("player2", "Player 2", 0, 1),
	}
	game.SetPlayers(users)

	// Set up player hands manually
	player1 := game.players[0]
	player2 := game.players[1]

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
	stateShowdown(game, nil)

	// Player 1 should win with pair of Aces
	if player1.Balance != 100 {
		t.Errorf("Expected player 1 to win with pot of 100, got %d", player1.Balance)
	}

	// Player 2 should not win anything
	if player2.Balance != 0 {
		t.Errorf("Expected player 2 to not win anything, got %d", player2.Balance)
	}

	// Check hand descriptions
	if !strings.Contains(player1.HandDescription, "Pair") {
		t.Errorf("Expected pair description, got %s", player1.HandDescription)
	}

	if !strings.Contains(player2.HandDescription, "High Card") {
		t.Errorf("Expected high card description, got %s", player2.HandDescription)
	}
}

func TestTieBreakerShowdown(t *testing.T) {
	// Create a game with 3 players
	cfg := GameConfig{
		NumPlayers: 3,
		Seed:       42,
	}

	game := NewGame(cfg)

	// Create test users and set them in the game
	users := []*User{
		NewUser("player1", "Player 1", 0, 0), // Start with 0 balance for clean test
		NewUser("player2", "Player 2", 0, 1),
		NewUser("player3", "Player 3", 0, 2),
	}
	game.SetPlayers(users)

	// Set up player hands manually
	player1 := game.players[0]
	player2 := game.players[1]
	player3 := game.players[2]

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
	stateShowdown(game, nil)

	// Players 1 and 2 should tie and split the pot (50 each)
	if player1.Balance != 50 {
		t.Errorf("Expected player 1 to win 50 (half pot), got %d", player1.Balance)
	}

	if player2.Balance != 50 {
		t.Errorf("Expected player 2 to win 50 (half pot), got %d", player2.Balance)
	}

	// Player 3 should not win anything (folded)
	if player3.Balance != 0 {
		t.Errorf("Expected player 3 to not win anything (folded), got %d", player3.Balance)
	}
}

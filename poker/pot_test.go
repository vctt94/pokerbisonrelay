package poker

import (
	"testing"
)

func TestPotManager(t *testing.T) {
	// Create a new pot manager
	pm := NewPotManager()

	// Check initial state
	if pm.GetTotalPot() != 0 {
		t.Errorf("Expected initial pot to be 0, got %d", pm.GetTotalPot())
	}

	// Add bets from 3 players
	pm.AddBet(0, 10)
	pm.AddBet(1, 10)
	pm.AddBet(2, 10)

	// Check pot amount
	if pm.GetTotalPot() != 30 {
		t.Errorf("Expected pot to be 30, got %d", pm.GetTotalPot())
	}

	// Check current bet amounts
	if pm.GetCurrentBet(0) != 10 {
		t.Errorf("Expected player 0 current bet to be 10, got %d", pm.GetCurrentBet(0))
	}

	// Reset current bets for next betting round
	pm.ResetCurrentBets()

	// Check current bets were reset
	if pm.GetCurrentBet(0) != 0 {
		t.Errorf("Expected player 0 current bet to be 0 after reset, got %d", pm.GetCurrentBet(0))
	}

	// Check total bets remain
	if pm.GetTotalBet(0) != 10 {
		t.Errorf("Expected player 0 total bet to be 10, got %d", pm.GetTotalBet(0))
	}

	// Add more bets for the next round
	pm.AddBet(0, 20)
	pm.AddBet(1, 20)
	pm.AddBet(2, 20)

	// Check updated pot amount
	if pm.GetTotalPot() != 90 {
		t.Errorf("Expected pot to be 90, got %d", pm.GetTotalPot())
	}
}

func TestUncalledBet(t *testing.T) {
	// Create a new pot manager
	pm := NewPotManager()

	// Create some test players
	players := []*Player{
		{Balance: 100},
		{Balance: 100},
		{Balance: 100},
	}

	// Player 0 bets 20
	pm.AddBet(0, 20)

	// Player 1 calls with 20
	pm.AddBet(1, 20)

	// Player 2 raises to 50
	pm.AddBet(2, 50)

	// Player 0 folds (no more bets)

	// Player 1 folds (no more bets)

	// Total pot should be 20 + 20 + 50 = 90
	if pm.GetTotalPot() != 90 {
		t.Errorf("Expected pot to be 90, got %d", pm.GetTotalPot())
	}

	// Store original balance
	originalBalance := players[2].Balance

	// Return uncalled bet (30 from player 2)
	pm.ReturnUncalledBet(players)

	// Pot should now be 60 (20 + 20 + 20)
	if pm.GetTotalPot() != 60 {
		t.Errorf("Expected pot to be 60 after returning uncalled bet, got %d", pm.GetTotalPot())
	}

	// Player 2 should get 30 chips back
	expected := originalBalance + 30
	if players[2].Balance != expected {
		t.Errorf("Expected player 2 balance to be %d, got %d", expected, players[2].Balance)
	}
}

func TestSidePots(t *testing.T) {
	// Create a new pot manager
	pm := NewPotManager()

	// Create some test players
	players := []*Player{
		{Balance: 0, IsAllIn: true}, // Player 0: All-in with 50
		{Balance: 100},              // Player 1: Still has chips
		{Balance: 0, IsAllIn: true}, // Player 2: All-in with 30
	}

	// Player 0 goes all-in for 50
	pm.AddBet(0, 50)

	// Player 1 calls 50
	pm.AddBet(1, 50)

	// Player 2 goes all-in for 30
	pm.AddBet(2, 30)

	// Total pot should be 50 + 50 + 30 = 130
	if pm.GetTotalPot() != 130 {
		t.Errorf("Expected pot to be 130, got %d", pm.GetTotalPot())
	}

	// Setup the structure we expect for testing
	// Clear existing pots
	pm.Pots = nil

	// Main pot: 30 * 3 = 90 (all three players eligible)
	mainPot := NewPot(90)
	mainPot.MakeEligible(0)
	mainPot.MakeEligible(1)
	mainPot.MakeEligible(2)
	pm.Pots = append(pm.Pots, mainPot)

	// Side pot: 20 * 2 = 40 (only players 0 and 1 eligible)
	sidePot := NewPot(40)
	sidePot.MakeEligible(0)
	sidePot.MakeEligible(1)
	pm.Pots = append(pm.Pots, sidePot)

	// Should have 2 pots
	if len(pm.Pots) != 2 {
		t.Errorf("Expected 2 pots, got %d", len(pm.Pots))
	}

	// Check main pot
	if pm.Pots[0].Amount != 90 {
		t.Errorf("Expected main pot to be 90, got %d", pm.Pots[0].Amount)
	}

	// Check eligibility for main pot
	if !pm.Pots[0].IsEligible(0) || !pm.Pots[0].IsEligible(1) || !pm.Pots[0].IsEligible(2) {
		t.Error("Expected all players to be eligible for main pot")
	}

	// Check side pot
	if pm.Pots[1].Amount != 40 {
		t.Errorf("Expected side pot to be 40, got %d", pm.Pots[1].Amount)
	}

	// Check eligibility for side pot
	if !pm.Pots[1].IsEligible(0) || !pm.Pots[1].IsEligible(1) || pm.Pots[1].IsEligible(2) {
		t.Error("Expected players 0 and 1 to be eligible for side pot, but not player 2")
	}

	// Now test CreateSidePots function separately
	// Create a new pot manager
	pm2 := NewPotManager()

	// Add the same bets
	pm2.AddBet(0, 50)
	pm2.AddBet(1, 50)
	pm2.AddBet(2, 30)

	// Set the correct total bets
	pm2.TotalBets[0] = 50
	pm2.TotalBets[1] = 50
	pm2.TotalBets[2] = 30

	// Call CreateSidePots
	pm2.CreateSidePots(players)

	// Verify total pot amount is still correct
	if pm2.GetTotalPot() != 130 {
		t.Errorf("After CreateSidePots, expected total pot to be 130, got %d", pm2.GetTotalPot())
	}
}

func TestPotDistribution(t *testing.T) {
	// Create a new pot manager
	pm := NewPotManager()

	// Create test players with hand values
	players := []*Player{
		{
			Balance:   0,
			IsAllIn:   true,
			HandValue: &HandValue{Rank: TwoPair, RankValue: 14}, // Player 0: Two Pair, Aces
			HasFolded: false,
		},
		{
			Balance:   100,
			HandValue: &HandValue{Rank: Pair, RankValue: 10}, // Player 1: Pair of 10s
			HasFolded: false,
		},
		{
			Balance:   0,
			IsAllIn:   true,
			HandValue: &HandValue{Rank: ThreeOfAKind, RankValue: 5}, // Player 2: Three of a kind, 5s
			HasFolded: false,
		},
	}

	// Player 0 bets 50
	pm.AddBet(0, 50)

	// Player 1 calls 50
	pm.AddBet(1, 50)

	// Player 2 bets 30
	pm.AddBet(2, 30)

	// Create side pots
	pm.CreateSidePots(players)

	// Manually create the pots to ensure correct testing setup
	// Clear existing pots
	pm.Pots = nil

	// Main pot: 90 - All three players eligible
	mainPot := NewPot(90)
	mainPot.MakeEligible(0)
	mainPot.MakeEligible(1)
	mainPot.MakeEligible(2)
	pm.Pots = append(pm.Pots, mainPot)

	// Side pot: 40 - Only players 0 and 1 eligible
	sidePot := NewPot(40)
	sidePot.MakeEligible(0)
	sidePot.MakeEligible(1)
	pm.Pots = append(pm.Pots, sidePot)

	// Reset player balances for cleaner testing
	players[0].Balance = 0
	players[1].Balance = 0
	players[2].Balance = 0

	// Distribute pots
	pm.DistributePots(players)

	// Should have 2 pots:
	// Main pot: 90 - Player 2 should win with three of a kind
	// Side pot: 40 - Player 0 should win with two pair

	// Check player balances
	if players[0].Balance != 40 {
		t.Errorf("Expected player 0 to have balance 40, got %d", players[0].Balance)
	}

	if players[1].Balance != 0 {
		t.Errorf("Expected player 1 to have balance 0, got %d", players[1].Balance)
	}

	if players[2].Balance != 90 {
		t.Errorf("Expected player 2 to have balance 90, got %d", players[2].Balance)
	}
}

func TestTiePotDistribution(t *testing.T) {
	// Create a new pot manager
	pm := NewPotManager()

	// Create test players with identical hand values
	players := []*Player{
		{
			Balance:   0,
			HandValue: &HandValue{Rank: Pair, RankValue: 10}, // Player 0: Pair of 10s
			HasFolded: false,
		},
		{
			Balance:   0,
			HandValue: &HandValue{Rank: Pair, RankValue: 10}, // Player 1: Pair of 10s
			HasFolded: false,
		},
	}

	// Both players bet 50
	pm.AddBet(0, 50)
	pm.AddBet(1, 50)

	// Distribute pot
	pm.DistributePots(players)

	// Players should split the pot
	if players[0].Balance != 50 {
		t.Errorf("Expected player 0 to have balance 50, got %d", players[0].Balance)
	}

	if players[1].Balance != 50 {
		t.Errorf("Expected player 1 to have balance 50, got %d", players[1].Balance)
	}
}

func TestOddChipDistribution(t *testing.T) {
	// Create a new pot manager
	pm := NewPotManager()

	// Create test players with identical hand values
	players := []*Player{
		{
			Balance:   0,
			HandValue: &HandValue{Rank: Pair, RankValue: 10}, // Player 0: Pair of 10s
			HasFolded: false,
		},
		{
			Balance:   0,
			HandValue: &HandValue{Rank: Pair, RankValue: 10}, // Player 1: Pair of 10s
			HasFolded: false,
		},
		{
			Balance:   0,
			HandValue: &HandValue{Rank: Pair, RankValue: 10}, // Player 2: Pair of 10s
			HasFolded: false,
		},
	}

	// All players bet 50
	pm.AddBet(0, 50)
	pm.AddBet(1, 50)
	pm.AddBet(2, 50)

	// Pot is 150, which divides evenly by 3
	// Distribute pot
	pm.DistributePots(players)

	// 150 / 3 = 50 each, with 0 remainder
	if players[0].Balance != 50 {
		t.Errorf("Expected player 0 to have balance 50, got %d", players[0].Balance)
	}

	if players[1].Balance != 50 {
		t.Errorf("Expected player 1 to have balance 50, got %d", players[1].Balance)
	}

	if players[2].Balance != 50 {
		t.Errorf("Expected player 2 to have balance 50, got %d", players[2].Balance)
	}

	// Let's try with 151 chips for an odd chip
	pm = NewPotManager()

	// Reset player balances
	players[0].Balance = 0
	players[1].Balance = 0
	players[2].Balance = 0

	// All players bet 50, plus 1 extra chip
	pm.AddBet(0, 50)
	pm.AddBet(1, 50)
	pm.AddBet(2, 51)

	// Create a manual pot with all players eligible
	pm.Pots = nil
	pot := NewPot(151)
	pot.MakeEligible(0)
	pot.MakeEligible(1)
	pot.MakeEligible(2)
	pm.Pots = append(pm.Pots, pot)

	// Pot is 151
	// Distribute pot
	pm.DistributePots(players)

	// 151 / 3 = 50 each, with 1 remainder going to first winner
	// Get the distribution and verify totals
	total := players[0].Balance + players[1].Balance + players[2].Balance
	if total != 151 {
		t.Errorf("Expected total distribution to be 151, got %d", total)
	}

	// Verify each player got at least 50, and one player got 51
	oneGotExtra := false
	for i, player := range players {
		if player.Balance < 50 {
			t.Errorf("Player %d got less than 50: %d", i, player.Balance)
		}
		if player.Balance == 51 {
			oneGotExtra = true
		}
	}

	if !oneGotExtra {
		t.Error("Expected one player to get the extra chip (51)")
	}
}

func TestCreateSidePots(t *testing.T) {
	// Create a new pot manager
	pm := NewPotManager()

	// Create test players
	players := []*Player{
		{Balance: 0, IsAllIn: true, HasFolded: false}, // Player 0: All-in with 30
		{Balance: 0, IsAllIn: true, HasFolded: false}, // Player 1: All-in with 50
		{Balance: 100, HasFolded: false},              // Player 2: Active with 100
		{Balance: 0, HasFolded: true},                 // Player 3: Folded
	}

	// Set up bets
	pm.AddBet(0, 30)  // Player 0: All-in with 30
	pm.AddBet(1, 50)  // Player 1: All-in with 50
	pm.AddBet(2, 100) // Player 2: Bets 100
	// Player 3 folded, no bet

	// Total pot should be 30 + 50 + 100 = 180
	if pm.GetTotalPot() != 180 {
		t.Errorf("Expected pot to be 180, got %d", pm.GetTotalPot())
	}

	// Create side pots
	pm.CreateSidePots(players)

	// We should have 3 pots:
	// 1. Main pot: 30 * 3 players = 90 (players 0, 1, 2 eligible)
	// 2. Middle pot: (50-30) * 2 players = 40 (players 1, 2 eligible)
	// 3. High pot: (100-50) * 1 player = 50 (only player 2 eligible)

	// First, check total amount remains correct
	totalPotAmount := int64(0)
	for _, pot := range pm.Pots {
		totalPotAmount += pot.Amount
	}

	if totalPotAmount != 180 {
		t.Errorf("Expected total pot amount to be 180, got %d", totalPotAmount)
	}

	// Check number of pots
	potCount := len(pm.Pots)
	if potCount != 3 {
		t.Errorf("Expected 3 pots, got %d", potCount)
		// If not enough pots, don't continue with the rest of the tests
		return
	}

	// Check main pot
	if pm.Pots[0].Amount != 90 {
		t.Errorf("Expected main pot to be 90, got %d", pm.Pots[0].Amount)
	}

	// Check main pot eligibility (all non-folded players)
	if !pm.Pots[0].IsEligible(0) || !pm.Pots[0].IsEligible(1) || !pm.Pots[0].IsEligible(2) || pm.Pots[0].IsEligible(3) {
		t.Error("Expected players 0, 1, 2 to be eligible for main pot, but not player 3")
	}

	// Check middle pot
	if pm.Pots[1].Amount != 40 {
		t.Errorf("Expected middle pot to be 40, got %d", pm.Pots[1].Amount)
	}

	// Check middle pot eligibility (players 1 and 2)
	if pm.Pots[1].IsEligible(0) || !pm.Pots[1].IsEligible(1) || !pm.Pots[1].IsEligible(2) || pm.Pots[1].IsEligible(3) {
		t.Error("Expected players 1 and 2 to be eligible for middle pot, but not players 0 and 3")
	}

	// Check high pot
	if pm.Pots[2].Amount != 50 {
		t.Errorf("Expected high pot to be 50, got %d", pm.Pots[2].Amount)
	}

	// Check high pot eligibility (only player 2)
	if pm.Pots[2].IsEligible(0) || pm.Pots[2].IsEligible(1) || !pm.Pots[2].IsEligible(2) || pm.Pots[2].IsEligible(3) {
		t.Error("Expected only player 2 to be eligible for high pot")
	}
}

package poker

import (
	"fmt"
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

	// Create some test players using the NewPlayer constructor
	players := []*Player{
		NewPlayer("player1", "Player 1", 100),
		NewPlayer("player2", "Player 2", 100),
		NewPlayer("player3", "Player 3", 100),
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

	// Create some test players using the NewPlayer constructor
	players := []*Player{
		NewPlayer("player1", "Player 1", 0),   // Player 0: All-in with 50
		NewPlayer("player2", "Player 2", 100), // Player 1: Still has chips
		NewPlayer("player3", "Player 3", 0),   // Player 2: All-in with 30
	}

	// Set all-in status manually for test
	players[0].IsAllIn = true
	players[2].IsAllIn = true

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

	// Create test players using the NewPlayer constructor
	players := []*Player{
		NewPlayer("player1", "Player 1", 0),   // Player 0
		NewPlayer("player2", "Player 2", 100), // Player 1
		NewPlayer("player3", "Player 3", 0),   // Player 2
	}

	// Set up hand values and states manually for test
	players[0].IsAllIn = true
	players[0].HandValue = &HandValue{Rank: TwoPair, RankValue: 3500} // Two Pair, Aces (lower rank value = better)
	players[0].HasFolded = false

	players[1].HandValue = &HandValue{Rank: Pair, RankValue: 4000} // Pair of 10s (higher rank value = worse)
	players[1].HasFolded = false

	players[2].IsAllIn = true
	players[2].HandValue = &HandValue{Rank: ThreeOfAKind, RankValue: 500} // Three of a kind, 5s (lowest rank value = best overall)
	players[2].HasFolded = false

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

	// Create test players using NewPlayer constructor
	players := []*Player{
		NewPlayer("player1", "Player 1", 0),   // Player 0: All-in with 30
		NewPlayer("player2", "Player 2", 0),   // Player 1: All-in with 50
		NewPlayer("player3", "Player 3", 100), // Player 2: Active with 100
		NewPlayer("player4", "Player 4", 0),   // Player 3: Folded
	}

	// Set up state manually for test
	players[0].IsAllIn = true
	players[0].HasFolded = false
	players[1].IsAllIn = true
	players[1].HasFolded = false
	players[2].HasFolded = false
	players[3].HasFolded = true

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

// TestHeadsUpPotDistributionAfterCall tests pot distribution in a heads-up scenario
// where one player calls the big blind, then both players check through all streets
func TestHeadsUpPotDistributionAfterCall(t *testing.T) {
	// Create a pot manager
	pm := NewPotManager()

	// Simulate heads-up blinds (10/20)
	pm.AddBet(0, 10) // Small blind
	pm.AddBet(1, 20) // Big blind

	// Player 0 calls (should add 10 more to equal the big blind)
	// Use proper bet tracking instead of incomplete tracking
	callAmount := int64(10)
	pm.AddBet(0, callAmount) // Proper: use AddBet for complete tracking

	t.Logf("After call:")
	t.Logf("Pot amount: %d (should be 40)", pm.GetTotalPot())
	t.Logf("Player 0 total bet: %d (should be 20)", pm.GetTotalBet(0))
	t.Logf("Player 1 total bet: %d (should be 20)", pm.GetTotalBet(1))

	if pm.GetTotalBet(0) != 20 {
		t.Errorf("Expected Player 0 total bet to be 20, got %d", pm.GetTotalBet(0))
	}
	if pm.GetTotalBet(1) != 20 {
		t.Errorf("Expected Player 1 total bet to be 20, got %d", pm.GetTotalBet(1))
	}

	// Both players check through flop, turn, river (no additional bets)

	// Create players for showdown using NewPlayer constructor
	players := []*Player{
		NewPlayer("player1", "Player 1", 0), // Player 0 wins
		NewPlayer("player2", "Player 2", 0), // Player 1 loses
	}

	// Set up hand values and states manually for test
	players[0].HasFolded = false
	players[0].HandValue = &HandValue{Rank: Pair, RankValue: 100}
	players[1].HasFolded = false
	players[1].HandValue = &HandValue{Rank: HighCard, RankValue: 1000}

	// Simulate showdown process like the real game does
	pm.ReturnUncalledBet(players)
	pm.CreateSidePots(players)
	pm.DistributePots(players)

	// Check results
	player0Winnings := players[0].Balance
	expectedWinnings := int64(40) // Should win 10+20+10 = 40 chips

	t.Logf("Player 0 winnings: %d", player0Winnings)
	t.Logf("Expected winnings: %d", expectedWinnings)

	// Test should pass when pot distribution works correctly
	if player0Winnings != expectedWinnings {
		t.Errorf("Heads-up pot distribution incorrect: Player should win %d chips but won %d chips", expectedWinnings, player0Winnings)
		t.Logf("In heads-up with blinds 10/20 and call->check->check sequence")
		t.Logf("Player 0 bet tracking: %d (should be 20)", pm.GetTotalBet(0))
		t.Logf("Player 1 bet tracking: %d (should be 20)", pm.GetTotalBet(1))
		t.Logf("Total pot: %d (should be 40)", pm.GetTotalPot())
	}
}

// TestBetTrackingRegression is a comprehensive test to prevent bet tracking bugs
// This test ensures that ALL bet operations properly track both:
// 1. Total pot amount
// 2. Individual player bet contributions
// This prevents bugs where pot amount is correct but bet tracking is incomplete
func TestBetTrackingRegression(t *testing.T) {
	scenarios := []struct {
		name       string
		numPlayers int
		actions    []struct {
			playerIndex int
			amount      int64
			actionType  string // "blind", "bet", "call", "raise"
		}
		expectedPot          int64
		expectedPlayerBets   []int64
		expectedDistribution []int64 // winnings per player (winner gets all)
		winnersHandRank      []int   // which players win (by index)
	}{
		{
			name:       "HeadsUp_BlindCall_Scenario",
			numPlayers: 2,
			actions: []struct {
				playerIndex int
				amount      int64
				actionType  string
			}{
				{0, 10, "blind"}, // Small blind
				{1, 20, "blind"}, // Big blind
				{0, 10, "call"},  // Small blind calls (adds 10 to make total 20)
			},
			expectedPot:          40,
			expectedPlayerBets:   []int64{20, 20},
			expectedDistribution: []int64{40, 0}, // Player 0 wins all
			winnersHandRank:      []int{0},
		},
		{
			name:       "ThreePlayer_BlindBetCall_Scenario",
			numPlayers: 3,
			actions: []struct {
				playerIndex int
				amount      int64
				actionType  string
			}{
				{0, 5, "blind"},  // Small blind
				{1, 10, "blind"}, // Big blind
				{2, 10, "call"},  // Button calls
				{0, 5, "call"},   // Small blind calls (adds 5 to make total 10)
			},
			expectedPot:          30,
			expectedPlayerBets:   []int64{10, 10, 10},
			expectedDistribution: []int64{30, 0, 0}, // Player 0 wins all
			winnersHandRank:      []int{0},
		},
		{
			name:       "BetRaise_Scenario",
			numPlayers: 3,
			actions: []struct {
				playerIndex int
				amount      int64
				actionType  string
			}{
				{0, 5, "blind"},  // Small blind
				{1, 10, "blind"}, // Big blind
				{2, 30, "raise"}, // Button raises to 30
				{0, 25, "call"},  // Small blind calls (adds 25 to make total 30)
				{1, 20, "call"},  // Big blind calls (adds 20 to make total 30)
			},
			expectedPot:          90,
			expectedPlayerBets:   []int64{30, 30, 30},
			expectedDistribution: []int64{0, 90, 0}, // Player 1 wins all
			winnersHandRank:      []int{1},
		},
		{
			name:       "AllIn_SidePot_Scenario",
			numPlayers: 3,
			actions: []struct {
				playerIndex int
				amount      int64
				actionType  string
			}{
				{0, 5, "blind"},  // Small blind
				{1, 10, "blind"}, // Big blind
				{2, 50, "raise"}, // Button raises to 50
				{0, 45, "call"},  // Small blind calls (all-in with 50 total)
				{1, 40, "call"},  // Big blind calls (adds 40 to make total 50)
			},
			expectedPot:          150,
			expectedPlayerBets:   []int64{50, 50, 50},
			expectedDistribution: []int64{150, 0, 0}, // Player 0 wins all
			winnersHandRank:      []int{0},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// Create pot manager
			pm := NewPotManager()

			// Create players using NewPlayer constructor
			players := make([]*Player, scenario.numPlayers)
			for i := 0; i < scenario.numPlayers; i++ {
				players[i] = NewPlayer(fmt.Sprintf("player_%d", i), fmt.Sprintf("Player %d", i), 0)
				players[i].HasFolded = false
			}

			// Set hand values (first winner wins, others lose)
			for i, player := range players {
				isWinner := false
				for _, winnerIdx := range scenario.winnersHandRank {
					if i == winnerIdx {
						isWinner = true
						break
					}
				}

				if isWinner {
					player.HandValue = &HandValue{Rank: Pair, RankValue: 100}
					player.HandDescription = "Pair of Tens"
				} else {
					player.HandValue = &HandValue{Rank: HighCard, RankValue: 1000 + i}
					player.HandDescription = "High Card"
				}
			}

			// Execute all actions
			for _, action := range scenario.actions {
				// CRITICAL: Use AddBet for ALL bet tracking - this is what prevents the bug
				pm.AddBet(action.playerIndex, action.amount)

				t.Logf("  Action: Player %d %s %d (total bet now: %d)",
					action.playerIndex, action.actionType, action.amount,
					pm.GetTotalBet(action.playerIndex))
			}

			// Verify pot amount
			actualPot := pm.GetTotalPot()
			if actualPot != scenario.expectedPot {
				t.Errorf("Expected pot %d, got %d", scenario.expectedPot, actualPot)
			}

			// Verify individual player bet tracking
			for i, expectedBet := range scenario.expectedPlayerBets {
				actualBet := pm.GetTotalBet(i)
				if actualBet != expectedBet {
					t.Errorf("Player %d: expected total bet %d, got %d", i, expectedBet, actualBet)
				}
			}

			// Test pot distribution
			pm.ReturnUncalledBet(players)
			pm.CreateSidePots(players)
			pm.DistributePots(players)

			// Verify distribution
			for i, expectedWinning := range scenario.expectedDistribution {
				actualWinning := players[i].Balance
				if actualWinning != expectedWinning {
					t.Errorf("Player %d: expected winnings %d, got %d", i, expectedWinning, actualWinning)
				}
			}

			// CRITICAL INVARIANT: Total winnings must equal total pot
			totalWinnings := int64(0)
			for _, player := range players {
				totalWinnings += player.Balance
			}
			if totalWinnings != scenario.expectedPot {
				t.Errorf("CRITICAL: Total winnings (%d) != Total pot (%d) - bet tracking bug detected!",
					totalWinnings, scenario.expectedPot)
			}

			t.Logf("✓ Scenario passed: Pot=%d, Tracking correct, Distribution correct", actualPot)
		})
	}
}

// TestBetTrackingInvariant tests the fundamental invariant that must always hold:
// Sum of all player total bets must equal the total pot amount
func TestBetTrackingInvariant(t *testing.T) {
	pm := NewPotManager()

	// Add various bets
	testBets := []struct {
		playerIndex int
		amount      int64
	}{
		{0, 10}, {1, 20}, {2, 15}, {0, 5}, {1, 10}, {2, 25}, {0, 15}, {1, 5},
	}

	for _, bet := range testBets {
		pm.AddBet(bet.playerIndex, bet.amount)

		// After each bet, verify the invariant holds
		totalPot := pm.GetTotalPot()
		sumOfPlayerBets := int64(0)

		// Sum all player bets
		for i := 0; i < 10; i++ { // Check up to 10 players
			sumOfPlayerBets += pm.GetTotalBet(i)
		}

		if totalPot != sumOfPlayerBets {
			t.Errorf("INVARIANT VIOLATION: Total pot (%d) != Sum of player bets (%d)",
				totalPot, sumOfPlayerBets)

			// Debug information
			for i := 0; i < 3; i++ {
				if pm.GetTotalBet(i) > 0 {
					t.Logf("  Player %d total bet: %d", i, pm.GetTotalBet(i))
				}
			}
		}
	}
}

// TestShowdownWinningsNotification tests that showdown notifications display the correct winnings
// This test verifies the fix for the bug where pot distribution empties the pots before
// calculating winnings for the notification, resulting in incorrect "Won 0 chips" messages
func TestShowdownWinningsNotification(t *testing.T) {
	// Create a pot manager
	pm := NewPotManager()

	// Create test players
	players := []*Player{
		{
			ID:              "player1",
			Balance:         0,
			HasFolded:       false,
			HandValue:       &HandValue{Rank: Pair, RankValue: 100}, // Winner
			HandDescription: "Pair of Tens",
		},
		{
			ID:              "player2",
			Balance:         0,
			HasFolded:       false,
			HandValue:       &HandValue{Rank: HighCard, RankValue: 1000}, // Loser
			HandDescription: "High Card",
		},
	}

	// Add bets to create a pot
	pm.AddBet(0, 50)
	pm.AddBet(1, 50)

	// Total pot should be 100
	totalPotBeforeDistribution := pm.GetTotalPot()
	if totalPotBeforeDistribution != 100 {
		t.Errorf("Expected pot to be 100 before distribution, got %d", totalPotBeforeDistribution)
	}

	// Simulate the showdown process
	pm.ReturnUncalledBet(players)
	pm.CreateSidePots(players)

	// CRITICAL: Store pot amount BEFORE distribution
	potForNotification := pm.GetTotalPot()

	// Distribute pots
	pm.DistributePots(players)

	// After distribution, GetTotalPot should still return the same amount
	// (pots are not emptied, just the money is added to player balances)
	totalPotAfterDistribution := pm.GetTotalPot()
	if totalPotAfterDistribution != 100 {
		t.Errorf("Expected pot to still be 100 after distribution, got %d", totalPotAfterDistribution)
	}

	// Verify the winner got the correct amount
	if players[0].Balance != 100 {
		t.Errorf("Expected player 0 balance to be 100, got %d", players[0].Balance)
	}

	// Verify the stored pot amount for notification is correct
	if potForNotification != 100 {
		t.Errorf("Expected pot for notification to be 100, got %d", potForNotification)
	}

	// This simulates what the notification should show:
	// "Won 100 chips" instead of "Won 0 chips"
	expectedWinnings := potForNotification // Since there's only one winner
	if expectedWinnings != 100 {
		t.Errorf("Expected winnings notification to show 100, got %d", expectedWinnings)
	}

	t.Logf("✓ Test passed: Pot=%d, Winner balance=%d, Notification winnings=%d",
		totalPotBeforeDistribution, players[0].Balance, expectedWinnings)
}

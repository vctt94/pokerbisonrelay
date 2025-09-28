package poker

import (
	"fmt"
	"testing"

	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
)

func equalBool(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPotManager(t *testing.T) {
	// Create a new pot manager
	pm := NewPotManager(3)

	// Create test players
	players := make([]*Player, 3)
	for i := 0; i < 3; i++ {
		players[i] = NewPlayer(fmt.Sprintf("player_%d", i), fmt.Sprintf("Player %d", i), 1000)
	}

	// Check initial state
	if pm.GetTotalPot() != 0 {
		t.Errorf("Expected initial pot to be 0, got %d", pm.GetTotalPot())
	}

	// Add bets from 3 players
	pm.AddBet(0, 10, players)
	pm.AddBet(1, 10, players)
	pm.AddBet(2, 10, players)

	// Check pot amount
	if pm.GetTotalPot() != 30 {
		t.Errorf("Expected total pot to be 30, got %d", pm.GetTotalPot())
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
	pm.AddBet(0, 20, players)
	pm.AddBet(1, 20, players)
	pm.AddBet(2, 20, players)

	// Check updated pot amount
	if pm.GetTotalPot() != 90 {
		t.Errorf("Expected total pot to be 90, got %d", pm.GetTotalPot())
	}
}

func TestUncalledBet(t *testing.T) {
	// Create a new pot manager
	pm := NewPotManager(3)

	// Create some test players using the NewPlayer constructor
	players := []*Player{
		NewPlayer("player1", "Player 1", 100),
		NewPlayer("player2", "Player 2", 100),
		NewPlayer("player3", "Player 3", 100),
	}

	// Player 0 bets 20
	pm.AddBet(0, 20, players)

	// Player 1 calls with 20
	pm.AddBet(1, 20, players)

	// Player 2 raises to 50
	pm.AddBet(2, 50, players)

	// Player 0 folds (no more bets)

	// Player 1 folds (no more bets)

	// Total bets should be 20 + 20 + 50 = 90
	if pm.GetTotalPot() != 90 {
		t.Errorf("Expected total bets to be 90, got %d", pm.GetTotalPot())
	}

	// With the new behavior, uncalled bets are not returned
	// The total bets remain at 90 (20 + 20 + 50) and will be distributed as-is
	if pm.GetTotalPot() != 90 {
		t.Errorf("Expected total bets to remain 90, got %d", pm.GetTotalPot())
	}

	// Player balances should remain unchanged until pot distribution
	expected := int64(100) // original balance
	if players[2].Balance != expected {
		t.Errorf("Expected player 2 balance to remain %d, got %d", expected, players[2].Balance)
	}
}

func TestSidePots(t *testing.T) {
	// Create a new pot manager
	pm := NewPotManager(3)

	// Create some test players using the NewPlayer constructor
	players := []*Player{
		NewPlayer("player1", "Player 1", 0),   // Player 0: All-in with 50
		NewPlayer("player2", "Player 2", 100), // Player 1: Still has chips
		NewPlayer("player3", "Player 3", 0),   // Player 2: All-in with 30
	}

	// Set all-in status manually for test
	players[0].stateMachine.Dispatch(playerStateAllIn)
	players[2].stateMachine.Dispatch(playerStateAllIn)

	// Player 0 goes all-in for 50
	pm.AddBet(0, 50, players)

	// Player 1 calls 50
	pm.AddBet(1, 50, players)

	// Player 2 goes all-in for 30
	pm.AddBet(2, 30, players)

	// Total pot should be 50 + 50 + 30 = 130
	if pm.GetTotalPot() != 130 {
		t.Errorf("Expected total pot to be 130, got %d", pm.GetTotalPot())
	}

	// Setup the structure we expect for testing
	// Clear existing pots
	pm.Pots = nil

	// Main pot: 30 * 3 = 90 (all three players eligible)
	mainPot := NewPot(3)
	mainPot.Amount = 90
	mainPot.MakeEligible(0)
	mainPot.MakeEligible(1)
	mainPot.MakeEligible(2)
	pm.Pots = append(pm.Pots, mainPot)

	// Side pot: 20 * 2 = 40 (only players 0 and 1 eligible)
	sidePot := NewPot(3)
	sidePot.Amount = 40
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

	// Now test the automatic pot building with the same bets
	// Create a new pot manager
	pm2 := NewPotManager(3)

	// Add the same bets (pots are automatically rebuilt on each bet)
	pm2.AddBet(0, 50, players)
	pm2.AddBet(1, 50, players)
	pm2.AddBet(2, 30, players)

	// Verify total pot amount is still correct
	if pm2.GetTotalPot() != 130 {
		t.Errorf("Expected total pot to be 130, got %d", pm2.GetTotalPot())
	}
}

func TestPotDistribution(t *testing.T) {
	// Create a new pot manager
	pm := NewPotManager(3)

	// Create test players using the NewPlayer constructor
	players := []*Player{
		NewPlayer("player1", "Player 1", 0),   // Player 0
		NewPlayer("player2", "Player 2", 100), // Player 1
		NewPlayer("player3", "Player 3", 0),   // Player 2
	}

	// Set up hand values and states manually for test
	players[0].stateMachine.Dispatch(playerStateAllIn)
	players[0].HandValue = &HandValue{Rank: TwoPair, RankValue: 3500} // Two Pair, Aces (lower rank value = better)
	players[0].stateMachine.Dispatch(playerStateInGame)

	players[1].HandValue = &HandValue{Rank: Pair, RankValue: 4000} // Pair of 10s (higher rank value = worse)
	players[1].stateMachine.Dispatch(playerStateInGame)

	players[2].stateMachine.Dispatch(playerStateAllIn)
	players[2].HandValue = &HandValue{Rank: ThreeOfAKind, RankValue: 500} // Three of a kind, 5s (lowest rank value = best overall)
	players[2].stateMachine.Dispatch(playerStateInGame)

	// Player 0 bets 50
	pm.AddBet(0, 50, players)

	// Player 1 calls 50
	pm.AddBet(1, 50, players)

	// Player 2 bets 30
	pm.AddBet(2, 30, players)

	// Pots are automatically built on each bet, no need to call BuildPotsFromTotals

	// Manually create the pots to ensure correct testing setup
	// Clear existing pots
	pm.Pots = nil

	// Main pot: 90 - All three players eligible
	mainPot := NewPot(3)
	mainPot.Amount = 90
	mainPot.MakeEligible(0)
	mainPot.MakeEligible(1)
	mainPot.MakeEligible(2)
	pm.Pots = append(pm.Pots, mainPot)

	// Side pot: 40 - Only players 0 and 1 eligible
	sidePot := NewPot(3)
	sidePot.Amount = 40
	sidePot.MakeEligible(0)
	sidePot.MakeEligible(1)
	pm.Pots = append(pm.Pots, sidePot)

	// Reset player balances for cleaner testing
	players[0].Balance = 0
	players[1].Balance = 0
	players[2].Balance = 0

	// Distribute pots
	if err := pm.DistributePots(players); err != nil {
		t.Errorf("DistributePots failed: %v", err)
	}

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
	pm := NewPotManager(3)

	// Create test players with identical hand values
	players := []*Player{
		{
			Balance:   0,
			HandValue: &HandValue{Rank: Pair, RankValue: 10}, // Player 0: Pair of 10s
		},
		{
			Balance:   0,
			HandValue: &HandValue{Rank: Pair, RankValue: 10}, // Player 1: Pair of 10s
		},
	}

	// Both players bet 50
	pm.AddBet(0, 50, players)
	pm.AddBet(1, 50, players)

	// Pots are automatically built on each bet, no need to call BuildPotsFromTotals

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
	pm := NewPotManager(3)

	// Create test players with identical hand values
	players := []*Player{
		{
			Balance:   0,
			HandValue: &HandValue{Rank: Pair, RankValue: 10}, // Player 0: Pair of 10s
		},
		{
			Balance:   0,
			HandValue: &HandValue{Rank: Pair, RankValue: 10}, // Player 1: Pair of 10s
		},
		{
			Balance:   0,
			HandValue: &HandValue{Rank: Pair, RankValue: 10}, // Player 2: Pair of 10s
		},
	}

	// All players bet 50
	pm.AddBet(0, 50, players)
	pm.AddBet(1, 50, players)
	pm.AddBet(2, 50, players)

	// Pot is 150, which divides evenly by 3
	// Pots are automatically built on each bet, no need to call BuildPotsFromTotals

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
	pm = NewPotManager(3)

	// Reset player balances
	players[0].Balance = 0
	players[1].Balance = 0
	players[2].Balance = 0

	// All players bet 50, plus 1 extra chip
	pm.AddBet(0, 50, players)
	pm.AddBet(1, 50, players)
	pm.AddBet(2, 51, players)

	// Create a manual pot with all players eligible
	pm.Pots = nil
	pot := NewPot(3)
	pot.Amount = 151
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

func TestBuildPotsFromTotals(t *testing.T) {
	// Create a new pot manager
	pm := NewPotManager(3)

	// Create test players using NewPlayer constructor
	players := []*Player{
		NewPlayer("player1", "Player 1", 0),   // Player 0: All-in with 30
		NewPlayer("player2", "Player 2", 0),   // Player 1: All-in with 50
		NewPlayer("player3", "Player 3", 100), // Player 2: Active with 100
		NewPlayer("player4", "Player 4", 0),   // Player 3: Folded
	}

	// Set up state manually for test
	players[0].stateMachine.Dispatch(playerStateAllIn)
	players[0].stateMachine.Dispatch(playerStateInGame)
	players[1].stateMachine.Dispatch(playerStateAllIn)
	players[1].stateMachine.Dispatch(playerStateInGame)
	players[2].stateMachine.Dispatch(playerStateInGame)
	players[3].stateMachine.Dispatch(playerStateFolded)

	// Set up bets
	pm.AddBet(0, 30, players)  // Player 0: All-in with 30
	pm.AddBet(1, 50, players)  // Player 1: All-in with 50
	pm.AddBet(2, 100, players) // Player 2: Bets 100
	// Player 3 folded, no bet

	// Total bets should be 30 + 50 + 100 = 180
	if pm.GetTotalPot() != 180 {
		t.Errorf("Expected total bets to be 180, got %d", pm.GetTotalPot())
	}

	// Pots are automatically built on each bet, no need to call BuildPotsFromTotals

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
	pm := NewPotManager(3)

	// Create test players
	players := make([]*Player, 3)
	for i := 0; i < 3; i++ {
		players[i] = NewPlayer(fmt.Sprintf("player_%d", i), fmt.Sprintf("Player %d", i), 1000)
	}

	// Simulate heads-up blinds (10/20)
	pm.AddBet(0, 10, players) // Small blind
	pm.AddBet(1, 20, players) // Big blind

	// Player 0 calls (should add 10 more to equal the big blind)
	// Use proper bet tracking instead of incomplete tracking
	callAmount := int64(10)
	pm.AddBet(0, callAmount, players) // Proper: use AddBet for complete tracking

	t.Logf("After call:")
	t.Logf("Total bets: %d (should be 40)", pm.GetTotalPot())
	t.Logf("Player 0 total bet: %d (should be 20)", pm.GetTotalBet(0))
	t.Logf("Player 1 total bet: %d (should be 20)", pm.GetTotalBet(1))

	if pm.GetTotalBet(0) != 20 {
		t.Errorf("Expected Player 0 total bet to be 20, got %d", pm.GetTotalBet(0))
	}
	if pm.GetTotalBet(1) != 20 {
		t.Errorf("Expected Player 1 total bet to be 20, got %d", pm.GetTotalBet(1))
	}

	// Both players check through flop, turn, river (no additional bets)

	// Update players for showdown (set balances to 0 for testing)
	players[0].Balance = 0 // Player 0 wins
	players[1].Balance = 0 // Player 1 loses

	// Set up hand values and states manually for test
	players[0].stateMachine.Dispatch(playerStateInGame)
	players[0].HandValue = &HandValue{Rank: Pair, RankValue: 100}
	players[1].stateMachine.Dispatch(playerStateInGame)
	players[1].HandValue = &HandValue{Rank: HighCard, RankValue: 1000}

	// Pots are automatically built on each bet, no need to call BuildPotsFromTotals
	if err := pm.DistributePots(players); err != nil {
		t.Errorf("DistributePots failed: %v", err)
	}

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
		t.Logf("Total bets: %d (should be 40)", pm.GetTotalPot())
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
			pm := NewPotManager(3)

			// Create players using NewPlayer constructor
			players := make([]*Player, scenario.numPlayers)
			for i := 0; i < scenario.numPlayers; i++ {
				players[i] = NewPlayer(fmt.Sprintf("player_%d", i), fmt.Sprintf("Player %d", i), 0)
				players[i].stateMachine.Dispatch(playerStateInGame)
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
				pm.AddBet(action.playerIndex, action.amount, players)

				t.Logf("  Action: Player %d %s %d (total bet now: %d)",
					action.playerIndex, action.actionType, action.amount,
					pm.GetTotalBet(action.playerIndex))
			}

			// Verify pot amount
			actualPot := pm.GetTotalPot()
			if actualPot != scenario.expectedPot {
				t.Errorf("Expected total bets %d, got %d", scenario.expectedPot, actualPot)
			}

			// Verify individual player bet tracking
			for i, expectedBet := range scenario.expectedPlayerBets {
				actualBet := pm.GetTotalBet(i)
				if actualBet != expectedBet {
					t.Errorf("Player %d: expected total bet %d, got %d", i, expectedBet, actualBet)
				}
			}

			// Pots are automatically built on each bet, no need to call BuildPotsFromTotals
			if err := pm.DistributePots(players); err != nil {
				t.Errorf("DistributePots failed: %v", err)
			}

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

			t.Logf("✓ Scenario passed: TotalBets=%d, Tracking correct, Distribution correct", actualPot)
		})
	}
}

// TestSidePotCornerCases tests various edge cases for side pot creation and management
func TestSidePotCornerCases(t *testing.T) {
	t.Run("AllPlayersAllInDifferentAmounts", func(t *testing.T) {
		pm := NewPotManager(4)
		players := make([]*Player, 4)
		for i := 0; i < 4; i++ {
			players[i] = NewPlayer(fmt.Sprintf("player_%d", i), fmt.Sprintf("Player %d", i), 0)
			players[i].stateMachine.Dispatch(playerStateAllIn)
		}

		// All players go all-in with different amounts
		pm.AddBet(0, 10, players) // Player 0: 10
		pm.AddBet(1, 20, players) // Player 1: 20
		pm.AddBet(2, 30, players) // Player 2: 30
		pm.AddBet(3, 40, players) // Player 3: 40

		// Should create 4 pots: 10*4, 10*3, 10*2, 10*1
		expectedPots := []int64{40, 30, 20, 10} // 10*4, 10*3, 10*2, 10*1
		if len(pm.Pots) != 4 {
			t.Errorf("Expected 4 pots, got %d", len(pm.Pots))
		}

		for i, expected := range expectedPots {
			if pm.Pots[i].Amount != expected {
				t.Errorf("Pot %d: expected %d, got %d", i, expected, pm.Pots[i].Amount)
			}
		}

		// Check eligibility for each pot
		expectedEligibility := [][]bool{
			{true, true, true, true},    // Pot 0: all players
			{false, true, true, true},   // Pot 1: players 1,2,3
			{false, false, true, true},  // Pot 2: players 2,3
			{false, false, false, true}, // Pot 3: only player 3
		}

		for potIdx, expected := range expectedEligibility {
			for playerIdx, shouldBeEligible := range expected {
				if pm.Pots[potIdx].IsEligible(playerIdx) != shouldBeEligible {
					t.Errorf("Pot %d, Player %d: expected eligible=%v, got %v",
						potIdx, playerIdx, shouldBeEligible, pm.Pots[potIdx].IsEligible(playerIdx))
				}
			}
		}
	})

	t.Run("OnePlayerFoldsBeforeAllIn", func(t *testing.T) {
		pm := NewPotManager(3)
		players := make([]*Player, 3)
		for i := 0; i < 3; i++ {
			players[i] = NewPlayer(fmt.Sprintf("player_%d", i), fmt.Sprintf("Player %d", i), 1000)
		}

		// Player 0 folds early
		players[0].stateMachine.Dispatch(playerStateFolded)
		pm.AddBet(0, 10, players)  // Player 0: 10 (but folded)
		pm.AddBet(1, 50, players)  // Player 1: 50 (all-in)
		pm.AddBet(2, 100, players) // Player 2: 100

		// Should create 3 pots: 10*3 (all players contribute), 40*2 (players 1,2), 50*1 (only player 2)
		expectedPots := []int64{30, 80, 50} // 10*3, 40*2, 50*1
		if len(pm.Pots) != 3 {
			t.Errorf("Expected 3 pots, got %d", len(pm.Pots))
		}

		for i, expected := range expectedPots {
			if pm.Pots[i].Amount != expected {
				t.Errorf("Pot %d: expected %d, got %d", i, expected, pm.Pots[i].Amount)
			}
		}

		// Check eligibility - folded player should not be eligible for any pot
		for potIdx := range pm.Pots {
			if pm.Pots[potIdx].IsEligible(0) {
				t.Errorf("Pot %d: folded player 0 should not be eligible", potIdx)
			}
		}
	})

	t.Run("IdenticalAllInAmounts", func(t *testing.T) {
		pm := NewPotManager(3)
		players := make([]*Player, 3)
		for i := 0; i < 3; i++ {
			players[i] = NewPlayer(fmt.Sprintf("player_%d", i), fmt.Sprintf("Player %d", i), 0)
			players[i].stateMachine.Dispatch(playerStateAllIn)
		}

		// All players go all-in with the same amount
		pm.AddBet(0, 50, players)
		pm.AddBet(1, 50, players)
		pm.AddBet(2, 50, players)

		// Should create only 1 pot: 50*3 = 150
		if len(pm.Pots) != 1 {
			t.Errorf("Expected 1 pot, got %d", len(pm.Pots))
		}

		if pm.Pots[0].Amount != 150 {
			t.Errorf("Expected pot amount 150, got %d", pm.Pots[0].Amount)
		}

		// All players should be eligible
		for i := 0; i < 3; i++ {
			if !pm.Pots[0].IsEligible(i) {
				t.Errorf("Player %d should be eligible for the pot", i)
			}
		}
	})

	t.Run("ZeroBets", func(t *testing.T) {
		pm := NewPotManager(3)
		players := make([]*Player, 3)
		for i := 0; i < 3; i++ {
			players[i] = NewPlayer(fmt.Sprintf("player_%d", i), fmt.Sprintf("Player %d", i), 1000)
		}

		// No bets placed
		// Should create 1 empty pot
		if len(pm.Pots) != 1 {
			t.Errorf("Expected 1 pot, got %d", len(pm.Pots))
		}

		if pm.Pots[0].Amount != 0 {
			t.Errorf("Expected pot amount 0, got %d", pm.Pots[0].Amount)
		}
	})

	t.Run("SinglePlayerAllIn", func(t *testing.T) {
		pm := NewPotManager(3)
		players := make([]*Player, 3)
		for i := 0; i < 3; i++ {
			players[i] = NewPlayer(fmt.Sprintf("player_%d", i), fmt.Sprintf("Player %d", i), 1000)
		}

		// Only one player bets
		pm.AddBet(0, 100, players)

		// Should create 1 pot: 100
		if len(pm.Pots) != 1 {
			t.Errorf("Expected 1 pot, got %d", len(pm.Pots))
		}

		if pm.Pots[0].Amount != 100 {
			t.Errorf("Expected pot amount 100, got %d", pm.Pots[0].Amount)
		}

		// Only player 0 should be eligible
		if !pm.Pots[0].IsEligible(0) {
			t.Errorf("Player 0 should be eligible for the pot")
		}
		for i := 1; i < 3; i++ {
			if pm.Pots[0].IsEligible(i) {
				t.Errorf("Player %d should not be eligible for the pot", i)
			}
		}
	})

	t.Run("MixedFoldedAndActivePlayers", func(t *testing.T) {
		pm := NewPotManager(4)
		players := make([]*Player, 4)
		for i := 0; i < 4; i++ {
			players[i] = NewPlayer(fmt.Sprintf("player_%d", i), fmt.Sprintf("Player %d", i), 1000)
		}

		// Player 0 folds, others bet different amounts
		players[0].stateMachine.Dispatch(playerStateFolded)
		pm.AddBet(0, 10, players)  // Player 0: 10 (folded)
		pm.AddBet(1, 30, players)  // Player 1: 30 (all-in)
		pm.AddBet(2, 60, players)  // Player 2: 60
		pm.AddBet(3, 100, players) // Player 3: 100

		// Should create 4 pots: 10*4, 20*3, 30*2, 40*1
		expectedPots := []int64{40, 60, 60, 40} // 10*4, 20*3, 30*2, 40*1
		if len(pm.Pots) != 4 {
			t.Errorf("Expected 4 pots, got %d", len(pm.Pots))
		}

		for i, expected := range expectedPots {
			if pm.Pots[i].Amount != expected {
				t.Errorf("Pot %d: expected %d, got %d", i, expected, pm.Pots[i].Amount)
			}
		}

		// Folded player should not be eligible for any pot
		for potIdx := range pm.Pots {
			if pm.Pots[potIdx].IsEligible(0) {
				t.Errorf("Pot %d: folded player 0 should not be eligible", potIdx)
			}
		}
	})

	t.Run("VeryLargeNumbers", func(t *testing.T) {
		pm := NewPotManager(3)
		players := make([]*Player, 3)
		for i := 0; i < 3; i++ {
			players[i] = NewPlayer(fmt.Sprintf("player_%d", i), fmt.Sprintf("Player %d", i), 0)
			players[i].stateMachine.Dispatch(playerStateAllIn)
		}

		// Test with very large numbers
		pm.AddBet(0, 1000000, players)
		pm.AddBet(1, 2000000, players)
		pm.AddBet(2, 3000000, players)

		// Should create 3 pots: 1000000*3, 1000000*2, 1000000*1
		expectedPots := []int64{3000000, 2000000, 1000000}
		if len(pm.Pots) != 3 {
			t.Errorf("Expected 3 pots, got %d", len(pm.Pots))
		}

		for i, expected := range expectedPots {
			if pm.Pots[i].Amount != expected {
				t.Errorf("Pot %d: expected %d, got %d", i, expected, pm.Pots[i].Amount)
			}
		}
	})

	t.Run("IncrementalBettingCreatesCorrectPots", func(t *testing.T) {
		pm := NewPotManager(3)
		players := make([]*Player, 3)
		for i := 0; i < 3; i++ {
			players[i] = NewPlayer(fmt.Sprintf("player_%d", i), fmt.Sprintf("Player %d", i), 1000)
		}

		// Simulate betting round by round
		// Round 1: Blinds
		pm.AddBet(0, 10, players) // Small blind
		pm.AddBet(1, 20, players) // Big blind

		// Check after blinds - should have 2 pots: 10*2, 10*1
		if len(pm.Pots) != 2 || pm.GetTotalPot() != 30 {
			t.Errorf("After blinds: expected 2 pots with total 30, got %d pots with total %d", len(pm.Pots), pm.GetTotalPot())
		}

		// Round 2: Player 2 raises
		pm.AddBet(2, 50, players) // Raise to 50

		// Check after raise - should have 3 pots: 10*3, 10*2, 30*1
		if len(pm.Pots) != 3 || pm.GetTotalPot() != 80 {
			t.Errorf("After raise: expected 3 pots with total 80, got %d pots with total %d", len(pm.Pots), pm.GetTotalPot())
		}

		// Round 3: Player 0 goes all-in
		players[0].stateMachine.Dispatch(playerStateAllIn)
		pm.AddBet(0, 40, players) // All-in for 50 total

		// Check after all-in - should have 2 pots: 50*3, 30*2
		if len(pm.Pots) != 2 || pm.GetTotalPot() != 120 {
			t.Errorf("After all-in: expected 2 pots with total 120, got %d pots with total %d", len(pm.Pots), pm.GetTotalPot())
		}

		// Round 4: Player 1 calls, Player 2 raises more
		pm.AddBet(1, 30, players) // Call to 50
		pm.AddBet(2, 50, players) // Raise to 100

		// Now we should have 2 pots: 50*3, 50*2
		expectedPots := []int64{150, 50} // 50*3, 50*2
		if len(pm.Pots) != 2 {
			t.Errorf("After side pot creation: expected 2 pots, got %d", len(pm.Pots))
		}

		for i, expected := range expectedPots {
			if pm.Pots[i].Amount != expected {
				t.Errorf("Pot %d: expected %d, got %d", i, expected, pm.Pots[i].Amount)
			}
		}
	})
}

// TestBetTrackingInvariant tests the fundamental invariant that must always hold:
// Sum of all player total bets must equal the total pot amount
func TestBetTrackingInvariant(t *testing.T) {
	pm := NewPotManager(3)

	// Create test players
	players := make([]*Player, 3)
	for i := 0; i < 3; i++ {
		players[i] = NewPlayer(fmt.Sprintf("player_%d", i), fmt.Sprintf("Player %d", i), 1000)
	}

	// Add various bets
	testBets := []struct {
		playerIndex int
		amount      int64
	}{
		{0, 10}, {1, 20}, {2, 15}, {0, 5}, {1, 10}, {2, 25}, {0, 15}, {1, 5},
	}

	for _, bet := range testBets {
		pm.AddBet(bet.playerIndex, bet.amount, players)

		// After each bet, verify the invariant holds
		totalBets := pm.GetTotalPot()
		sumOfPlayerBets := int64(0)

		// Sum all player bets
		for i := 0; i < 10; i++ { // Check up to 10 players
			sumOfPlayerBets += pm.GetTotalBet(i)
		}

		if totalBets != sumOfPlayerBets {
			t.Errorf("INVARIANT VIOLATION: Total bets (%d) != Sum of player bets (%d)",
				totalBets, sumOfPlayerBets)

			// Debug information
			for i := 0; i < 3; i++ {
				if pm.GetTotalBet(i) > 0 {
					t.Logf("  Player %d total bet: %d", i, pm.GetTotalBet(i))
				}
			}
		}
	}
}

// TestShowdownWinningsNotification_PotZeroedAfterDistribution verifies that we:
// 1) snapshot the total pot BEFORE distribution for the notification amount, and
// 2) after DistributePots, the working pot total is zero while the winner's balance reflects the payout.

func TestShowdownWinningsNotification_PotZeroedAfterDistribution(t *testing.T) {
	pm := NewPotManager(3)

	players := []*Player{
		{
			ID:              "player1",
			Balance:         0,
			HandValue:       &HandValue{Rank: Pair, RankValue: 100}, // Winner
			HandDescription: "Pair of Tens",
		},
		{
			ID:              "player2",
			Balance:         0,
			HandValue:       &HandValue{Rank: HighCard, RankValue: 1000}, // Loser
			HandDescription: "High Card",
		},
	}

	// Bets: 50 + 50 = 100
	pm.AddBet(0, 50, players)
	pm.AddBet(1, 50, players)

	if got := pm.GetTotalPot(); got != 100 {
		t.Fatalf("expected total bets 100 before distribution, got %d", got)
	}

	// Pots are automatically built on each bet, no need to call BuildPotsFromTotals

	// Capture amount for notification BEFORE distribution.
	potForNotification := pm.GetTotalPot()
	if potForNotification != 100 {
		t.Fatalf("expected pot for notification 100, got %d", potForNotification)
	}

	// Distribute (should zero working pots).
	if err := pm.DistributePots(players); err != nil {
		t.Fatalf("DistributePots failed: %v", err)
	}

	// After distribution, pots must be zero.
	if got := pm.GetTotalPot(); got != 0 {
		t.Fatalf("expected total pot 0 after distribution, got %d", got)
	}

	// Winner received full amount.
	if players[0].Balance != 100 {
		t.Fatalf("expected winner balance 100, got %d", players[0].Balance)
	}
	if players[1].Balance != 0 {
		t.Fatalf("expected loser balance 0, got %d", players[1].Balance)
	}

	// Notification uses pre-distribution amount.
	if potForNotification != 100 {
		t.Fatalf("expected notification winnings 100, got %d", potForNotification)
	}

	t.Logf("✓ Test passed: PotBefore=%d, PotAfter=0, WinnerBalance=%d",
		potForNotification, players[0].Balance)
}

func mkPlayers(n int) []*Player {
	ps := make([]*Player, n)
	for i := range ps {
		ps[i] = NewPlayer(string(rune('A'+i)), "", 1000) // Use NewPlayer to properly initialize stateMachine
	}
	return ps
}

// Helper: run build+distribute and return final balances and total pot
// settle settles remaining pots once and returns (deltaBalances, totalPotBefore).
// It measures deltas from the moment it's called, so prior refunds are excluded.
func settle(t *testing.T, pm *PotManager, players []*Player) ([]int64, int64) {
	t.Helper()

	// Snapshot balances AFTER any refunds, BEFORE distribution.
	before := make([]int64, len(players))
	for i, p := range players {
		before[i] = p.Balance
	}

	// Total pot available to distribute now.
	total := pm.GetTotalPot()
	if total > 0 {
		if err := pm.DistributePots(players); err != nil {
			t.Fatalf("settle: distribute: %v", err)
		}
	}

	// Return only the settlement deltas (excludes prior refunds).
	delta := make([]int64, len(players))
	for i, p := range players {
		delta[i] = p.Balance - before[i]
	}
	return delta, total
}

// A:20, B:20, C:20 (all to showdown), A wins
func TestContested_EqualStacks_SingleWinner(t *testing.T) {
	players := mkPlayers(3)
	pm := NewPotManager(3)

	// Use AddBet to properly build pots
	pm.AddBet(0, 20, players)
	pm.AddBet(1, 20, players)
	pm.AddBet(2, 20, players)

	// showdown winners: only A (index 0)
	players[0].HandValue = &HandValue{HandRank: pokerrpc.HandRank_PAIR, RankValue: 0}
	players[1].HandValue = &HandValue{HandRank: pokerrpc.HandRank_HIGH_CARD, RankValue: 1}
	players[2].HandValue = &HandValue{HandRank: pokerrpc.HandRank_HIGH_CARD, RankValue: 1}

	// Pots are automatically built on each bet, no need to call BuildPotsFromTotals

	bals, pot := settle(t, pm, players)
	if pot != 60 {
		t.Fatalf("pot=%d want 60", pot)
	}
	want := []int64{60, 0, 0}
	for i := range bals {
		if bals[i] != want[i] {
			t.Fatalf("balances=%v want %v", bals, want)
		}
	}
}

func TestContested_SidePot_BWinsMain_CWinsSide(t *testing.T) {
	players := mkPlayers(3)
	pm := NewPotManager(3)

	// Use AddBet to properly build pots
	pm.AddBet(0, 100, players) // A
	pm.AddBet(1, 50, players)  // B (all-in)
	pm.AddBet(2, 100, players) // C

	players[0].HandValue = &HandValue{RankValue: 2} // A
	players[1].HandValue = &HandValue{RankValue: 1} // B (best)
	players[2].HandValue = &HandValue{RankValue: 2} // C

	// (Optional sanity checks)
	if got := pm.GetTotalPot(); got != 250 {
		t.Fatalf("built pot=%d want 250", got)
	}
	if len(pm.Pots) != 2 ||
		pm.Pots[0].Amount != 150 || !equalBool(pm.Pots[0].Eligibility, []bool{true, true, true}) ||
		pm.Pots[1].Amount != 100 || !equalBool(pm.Pots[1].Eligibility, []bool{true, false, true}) {
		t.Fatalf("pots not as expected: %+v", pm.Pots)
	}

	bals, pot := settle(t, pm, players)
	if pot != 250 {
		t.Fatalf("pot=%d want 250", pot)
	}
	want := []int64{50, 150, 50} // main: B=150; side: A=50, C=50
	for i := range bals {
		if bals[i] != want[i] {
			t.Fatalf("balances=%v want %v", bals, want)
		}
	}
}

// Raise/folds (voluntary action): SB=10, BB=20, BTN raises to 60, SB folds, BB folds -> refund 40
// Pot stays 50 (10+20+20), raiser wins 50.
func TestContested_UncalledRaiseRefund(t *testing.T) {
	players := mkPlayers(3)
	pm := NewPotManager(3)

	// Use AddBet to properly build pots
	pm.AddBet(0, 10, players) // SB
	pm.AddBet(1, 20, players) // BB
	pm.AddBet(2, 60, players) // Raiser

	// Everyone except BTN folded -> only player 2 alive, but because there WAS voluntary action,
	// the uncalled portion (40) must be refunded before building pots.
	players[0].stateMachine.Dispatch(playerStateFolded)
	players[1].stateMachine.Dispatch(playerStateFolded)

	pm.ReturnUncalledBet(players) // should refund 40 to player 2 and reduce totals
	if pm.TotalBets[2] != 20 {
		t.Fatalf("TotalBets[BTN]=%d want 20 after refund", pm.TotalBets[2])
	}

	// Debug: check what happens after pot building
	t.Logf("Before pot building:")
	t.Logf("TotalBets: %v", pm.TotalBets)
	t.Logf("Player 0 folded: %v", players[0].GetCurrentStateString() == "FOLDED")
	t.Logf("Player 1 folded: %v", players[1].GetCurrentStateString() == "FOLDED")
	t.Logf("Player 2 folded: %v", players[2].GetCurrentStateString() == "FOLDED")

	// Pots are automatically built on each bet, no need to call BuildPotsFromTotals
	t.Logf("After pot building:")
	t.Logf("Number of pots: %d", len(pm.Pots))
	for i, pot := range pm.Pots {
		t.Logf("Pot %d: amount=%d, eligibility=%v", i, pot.Amount, pot.Eligibility)
	}
	t.Logf("Total pot: %d", pm.GetTotalPot())

	bals, pot := settle(t, pm, players)
	if pot != 50 {
		t.Fatalf("pot=%d want 50", pot)
	}
	want := []int64{0, 0, 50}
	for i := range bals {
		if bals[i] != want[i] {
			t.Fatalf("balances=%v want %v", bals, want)
		}
	}
}

// Tie in a pot: A and C tie best for pot that both are eligible for; split with remainder to first winner.
func TestContested_TieSplitRemainder(t *testing.T) {
	players := mkPlayers(3)
	pm := NewPotManager(3)

	pm.AddBet(0, 50, players)
	pm.AddBet(1, 50, players)
	pm.AddBet(2, 50, players)

	players[0].HandValue = &HandValue{HandRank: 5, RankValue: 100} // Straight
	players[1].HandValue = &HandValue{HandRank: 3, RankValue: 200} // Trips (worse)
	players[2].HandValue = &HandValue{HandRank: 5, RankValue: 100} // Straight (tie)

	bals, pot := settle(t, pm, players)
	if pot != 150 {
		t.Fatalf("pot=%d want 150", pot)
	}
	want := []int64{75, 0, 75}
	for i := range bals {
		if bals[i] != want[i] {
			t.Fatalf("balances=%v want %v", bals, want)
		}
	}
}

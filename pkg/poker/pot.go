package poker

// Pot represents a pot of chips in the game
type Pot struct {
	Amount      int64        // Total amount in the pot
	Eligibility map[int]bool // Player indices that are eligible to win this pot
}

// NewPot creates a new pot with the given amount
func NewPot(amount int64) *Pot {
	return &Pot{
		Amount:      amount,
		Eligibility: make(map[int]bool),
	}
}

// MakeEligible marks a player as eligible to win this pot
func (p *Pot) MakeEligible(playerIndex int) {
	p.Eligibility[playerIndex] = true
}

// IsEligible checks if a player is eligible to win this pot
func (p *Pot) IsEligible(playerIndex int) bool {
	return p.Eligibility[playerIndex]
}

// PotManager manages multiple pots, including the main pot and side pots
type PotManager struct {
	Pots        []*Pot        // Main pot followed by side pots
	CurrentBets map[int]int64 // Current bet for each player in this round
	TotalBets   map[int]int64 // Total bet for each player across all rounds
}

// NewPotManager creates a new pot manager
func NewPotManager() *PotManager {
	return &PotManager{
		Pots:        []*Pot{NewPot(0)}, // Start with an empty main pot
		CurrentBets: make(map[int]int64),
		TotalBets:   make(map[int]int64),
	}
}

// AddBet adds a bet to the pot manager
func (pm *PotManager) AddBet(playerIndex int, amount int64) {
	// Add to player's current bet
	pm.CurrentBets[playerIndex] += amount

	// Add to player's total bet
	pm.TotalBets[playerIndex] += amount

	// Add to main pot for now
	pm.Pots[0].Amount += amount

	// Mark player as eligible for the pot
	pm.Pots[0].MakeEligible(playerIndex)
}

// ResetCurrentBets resets the current bets for a new betting round
func (pm *PotManager) ResetCurrentBets() {
	pm.CurrentBets = make(map[int]int64)
}

// GetMainPot returns the main pot
func (pm *PotManager) GetMainPot() *Pot {
	return pm.Pots[0]
}

// GetTotalPot returns the total amount across all pots
func (pm *PotManager) GetTotalPot() int64 {
	var total int64
	for _, pot := range pm.Pots {
		total += pot.Amount
	}
	return total
}

// GetCurrentBet returns the current bet for a player
func (pm *PotManager) GetCurrentBet(playerIndex int) int64 {
	return pm.CurrentBets[playerIndex]
}

// GetTotalBet returns the total bet for a player across all rounds
func (pm *PotManager) GetTotalBet(playerIndex int) int64 {
	return pm.TotalBets[playerIndex]
}

// CreateSidePots creates side pots based on all-in players
func (pm *PotManager) CreateSidePots(players []*Player) {
	// Collect all bets by size
	betAmounts := make(map[int64]bool)
	for _, bet := range pm.TotalBets {
		if bet > 0 {
			betAmounts[bet] = true
		}
	}

	// Extract unique bet sizes and sort them
	uniqueBets := make([]int64, 0, len(betAmounts))
	for bet := range betAmounts {
		uniqueBets = append(uniqueBets, bet)
	}

	// Sort bets in ascending order
	for i := 0; i < len(uniqueBets); i++ {
		for j := i + 1; j < len(uniqueBets); j++ {
			if uniqueBets[i] > uniqueBets[j] {
				uniqueBets[i], uniqueBets[j] = uniqueBets[j], uniqueBets[i]
			}
		}
	}

	// If there's only one bet size, no side pots needed
	if len(uniqueBets) <= 1 {
		return
	}

	// Create the new pots
	var pots []*Pot
	var prevBet int64 = 0

	// Iterate through bet sizes from lowest to highest
	for i, bet := range uniqueBets {
		// Create a pot for this level
		pot := NewPot(0)

		// Calculate pot amount and determine eligible players
		for playerIdx, playerBet := range pm.TotalBets {
			if playerBet >= bet && !players[playerIdx].HasFolded {
				// Player is eligible for this pot
				pot.MakeEligible(playerIdx)
			}

			// Calculate contribution to this pot level
			if playerBet > prevBet {
				contribution := playerBet
				if playerBet > bet {
					contribution = bet
				}
				contribution -= prevBet

				// Add contribution to pot
				pot.Amount += contribution
			}
		}

		// Add this pot to our collection
		pots = append(pots, pot)

		// Update previous bet level
		prevBet = bet

		// If this is the last level, create a final pot for anything above it
		if i == len(uniqueBets)-1 {
			// Check if there are any bets above the highest all-in
			hasHigherBets := false
			for _, playerBet := range pm.TotalBets {
				if playerBet > bet {
					hasHigherBets = true
					break
				}
			}

			if hasHigherBets {
				// Create final pot
				finalPot := NewPot(0)

				// Calculate pot amount and eligible players
				for playerIdx, playerBet := range pm.TotalBets {
					if playerBet > bet && !players[playerIdx].HasFolded {
						// Player is eligible for this pot
						finalPot.MakeEligible(playerIdx)

						// Add contribution
						finalPot.Amount += (playerBet - bet)
					}
				}

				// Add final pot
				pots = append(pots, finalPot)
			}
		}
	}

	// Replace existing pots with our newly created ones
	pm.Pots = pots
}

// DistributePots distributes all pots to the winners
func (pm *PotManager) DistributePots(players []*Player) {
	for _, pot := range pm.Pots {
		// Find the best hand among eligible players
		var winners []int
		var bestHand *HandValue

		for playerIndex, isEligible := range pot.Eligibility {
			player := players[playerIndex]

			if isEligible && !player.HasFolded && player.HandValue != nil {
				if bestHand == nil || CompareHands(*player.HandValue, *bestHand) > 0 {
					bestHand = player.HandValue
					winners = []int{playerIndex}
				} else if bestHand != nil && CompareHands(*player.HandValue, *bestHand) == 0 {
					// It's a tie
					winners = append(winners, playerIndex)
				}
			}
		}

		// Distribute this pot among the winners
		if len(winners) > 0 {
			winnings := pot.Amount / int64(len(winners))
			remainder := pot.Amount % int64(len(winners))

			for _, winnerIndex := range winners {
				players[winnerIndex].Balance += winnings

				// If there's a remainder, give it to the first winner
				if remainder > 0 && winnerIndex == winners[0] {
					players[winnerIndex].Balance += remainder
				}
			}
		}

		// For debugging
		if len(winners) == 0 {
			// This should never happen in a normal game
			// All eligible players folded or no eligible players
			// In a real implementation, this would be an error condition
		}
	}
}

// ReturnUncalledBet returns any uncalled portion of a bet to the player who made it
func (pm *PotManager) ReturnUncalledBet(players []*Player) {
	// Find the highest and second-highest bets
	var highestBet, secondHighestBet int64
	var highestBetPlayer int

	for playerIndex, bet := range pm.CurrentBets {
		if bet > highestBet {
			secondHighestBet = highestBet
			highestBet = bet
			highestBetPlayer = playerIndex
		} else if bet > secondHighestBet {
			secondHighestBet = bet
		}
	}

	// If the highest bet is uncalled, return the difference
	if highestBet > secondHighestBet {
		uncalledAmount := highestBet - secondHighestBet

		// Remove from main pot
		pm.Pots[0].Amount -= uncalledAmount

		// Return to player
		players[highestBetPlayer].Balance += uncalledAmount

		// Adjust player's bets
		pm.CurrentBets[highestBetPlayer] -= uncalledAmount
		pm.TotalBets[highestBetPlayer] -= uncalledAmount
	}
}

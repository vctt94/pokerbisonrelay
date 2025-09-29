package poker

import (
	"fmt"
	"sort"
)

// Pot represents a pot of chips in the game
type Pot struct {
	Amount      int64  // Total amount in the pot
	Eligibility []bool // len == len(players); seat-aligned mask
}

// NewPot creates a new pot with the given amount
func NewPot(nPlayers int) *Pot {
	return &Pot{
		Amount:      0,
		Eligibility: make([]bool, nPlayers),
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

func NewPotManager(nPlayers int) *PotManager {
	return &PotManager{
		Pots:        []*Pot{NewPot(nPlayers)}, // placeholder; real amounts built later
		CurrentBets: make(map[int]int64),
		TotalBets:   make(map[int]int64),
	}
}

// AddBet adds a bet and immediately rebuilds pots to handle side pot creation
func (pm *PotManager) AddBet(playerIndex int, amount int64, players []*Player) {
	pm.CurrentBets[playerIndex] += amount
	pm.TotalBets[playerIndex] += amount
	pm.RebuildPotsIncremental(players)
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

// RebuildPotsIncremental rebuilds pots based on current TotalBets and player status.
// Called after each bet to maintain proper side-pot structure.
func (pm *PotManager) RebuildPotsIncremental(players []*Player) {
	n := len(players)
	if n == 0 {
		pm.Pots = []*Pot{NewPot(0)}
		return
	}

	// Collect unique positive bet thresholds.
	seen := make(map[int64]struct{}, n)
	for i := 0; i < n; i++ {
		if b := pm.TotalBets[i]; b > 0 {
			seen[b] = struct{}{}
		}
	}

	// If everyone is at 0, a single empty pot is enough.
	if len(seen) == 0 {
		pm.Pots = []*Pot{NewPot(n)}
		return
	}

	// Sort thresholds ascending.
	levels := make([]int64, 0, len(seen))
	for b := range seen {
		levels = append(levels, b)
	}
	sort.Slice(levels, func(i, j int) bool { return levels[i] < levels[j] })

	pots := make([]*Pot, 0, len(levels)+1)
	prev := int64(0)

	// Build capped pots for each threshold.
	for _, lvl := range levels {
		p := NewPot(n)
		amt := int64(0)

		for i := 0; i < n; i++ {
			tb := pm.TotalBets[i]
			// Eligible if not folded and contributed at least to this level.
			if players[i] != nil && !(players[i].GetCurrentStateString() == "FOLDED") && tb >= lvl {
				p.Eligibility[i] = true
			}
			// Contribution into this layer is clamp(tb, prev..lvl) - prev.
			if tb > prev {
				upTo := tb
				if upTo > lvl {
					upTo = lvl
				}
				if upTo > prev {
					amt += (upTo - prev)
				}
			}
		}

		p.Amount = amt
		pots = append(pots, p)
		prev = lvl
	}

	// Final uncapped overage pot above the highest level (e.g., raises not all-in capped).
	top := levels[len(levels)-1]
	over := NewPot(n)
	hasOver := false

	for i := 0; i < n; i++ {
		tb := pm.TotalBets[i]
		if tb > top {
			over.Amount += tb - top
			if players[i] != nil && !(players[i].GetCurrentStateString() == "FOLDED") {
				over.Eligibility[i] = true
			}
			hasOver = true
		}
	}
	if hasOver {
		pots = append(pots, over)
	}
	pm.Pots = pots
}

// DistributePots distributes all pots to showdown winners.
// Robust to accidental calls on uncontested pots and idempotent:
// pots are zeroed after payout so re-entry is a no-op.
// DistributePots pays out all pots. Safe to call multiple times (pots are zeroed after payout).
func (pm *PotManager) DistributePots(players []*Player) error {
	for pi, pot := range pm.Pots {
		// Idempotent: skip empty/already-settled pots.
		if pot.Amount <= 0 {
			continue
		}

		// Collect eligible & not-folded players.
		if len(pot.Eligibility) != len(players) {
			return fmt.Errorf("[pot %d] eligibility len %d != players len %d",
				pi, len(pot.Eligibility), len(players))
		}
		var alive []int
		for idx, elig := range pot.Eligibility {
			if idx < 0 || idx >= len(players) {
				return fmt.Errorf("[pot %d] eligibility idx %d out of range (players=%d)", pi, idx, len(players))
			}
			if elig && players[idx] != nil && !(players[idx].GetCurrentStateString() == "FOLDED") {
				alive = append(alive, idx)
			}
		}

		// Uncontested pot path.
		if len(alive) == 1 {
			w := alive[0]
			players[w].Balance += pot.Amount
			pm.Pots[pi].Amount = 0
			for j := range pm.Pots[pi].Eligibility {
				pm.Pots[pi].Eligibility[j] = false
			}
			continue
		}
		if len(alive) == 0 {
			return fmt.Errorf("[pot %d] no eligible alive players; pot=%d", pi, pot.Amount)
		}

		// Showdown: find best hand(s) safely.
		var winners []int
		var best *HandValue
		for _, idx := range alive {
			hv := players[idx].HandValue
			if hv == nil {
				return fmt.Errorf("[pot %d] player %d eligible at showdown but HandValue == nil", pi, idx)
			}
			if best == nil {
				best = hv
				winners = []int{idx}
				continue
			}
			cmp := CompareHands(*hv, *best)
			if cmp > 0 {
				best = hv
				winners = []int{idx}
			} else if cmp == 0 {
				winners = append(winners, idx)
			}
		}
		if len(winners) == 0 {
			return fmt.Errorf("[pot %d] showdown produced no winners", pi)
		}

		// Split pot; first winner gets remainder.
		share := pot.Amount / int64(len(winners))
		rem := pot.Amount % int64(len(winners))
		for i, idx := range winners {
			add := share
			if i == 0 && rem > 0 {
				add += rem
			}
			players[idx].Balance += add
		}

		// Mark pot as settled.
		pm.Pots[pi].Amount = 0
		for j := range pm.Pots[pi].Eligibility {
			pm.Pots[pi].Eligibility[j] = false
		}
	}
	return nil
}

// ReturnUncalledBet returns any uncalled portion of a bet to the player who made it
func (pm *PotManager) ReturnUncalledBet(players []*Player) {
	var hi, second int64
	hiPlayer := -1

	for idx, bet := range pm.CurrentBets {
		if bet > hi {
			second = hi
			hi = bet
			hiPlayer = idx
		} else if bet > second {
			second = bet
		}
	}

	if hiPlayer >= 0 && hi > second {
		uncalled := hi - second
		players[hiPlayer].Balance += uncalled
		pm.CurrentBets[hiPlayer] -= uncalled
		pm.TotalBets[hiPlayer] -= uncalled

		// Rebuild pots after refund to reflect the new totals
		pm.RebuildPotsIncremental(players)
	}
}

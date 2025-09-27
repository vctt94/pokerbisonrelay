package poker

import (
	"github.com/decred/slog"
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
	log         slog.Logger
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

// Track bets only; DO NOT touch pm.Pots here.
func (pm *PotManager) AddBet(playerIndex int, amount int64) {
	pm.CurrentBets[playerIndex] += amount
	pm.TotalBets[playerIndex] += amount
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

// BuildPotsFromTotals rebuilds main/side pots from TotalBets and fold status.
// Call after ReturnUncalledBet (if any) and before distribution.
func (pm *PotManager) BuildPotsFromTotals(players []*Player) {
	n := len(players)

	// Collect unique bet thresholds (excluding zero unless all zero)
	seen := map[int64]bool{}
	for i := 0; i < n; i++ {
		b := pm.TotalBets[i]
		if b > 0 {
			seen[b] = true
		}
	}
	if len(seen) == 0 {
		pm.Pots = []*Pot{NewPot(n)}
		return
	}

	levels := make([]int64, 0, len(seen))
	for b := range seen {
		levels = append(levels, b)
	}
	// sort ascending (tiny in-place sort)
	for i := 0; i < len(levels); i++ {
		for j := i + 1; j < len(levels); j++ {
			if levels[i] > levels[j] {
				levels[i], levels[j] = levels[j], levels[i]
			}
		}
	}

	pots := make([]*Pot, 0, len(levels)+1)
	prev := int64(0)

	for _, lvl := range levels {
		p := NewPot(n)
		// eligibility: any non-folded player who contributed at least lvl
		for i := 0; i < n; i++ {
			if players[i] != nil && !players[i].HasFolded && pm.TotalBets[i] >= lvl {
				p.Eligibility[i] = true
			}
		}
		// contributions: each player pays min(TotalBets[i], lvl) - prev
		for i := 0; i < n; i++ {
			tb := pm.TotalBets[i]
			if tb > prev {
				c := tb
				if c > lvl {
					c = lvl
				}
				c -= prev
				if c > 0 {
					p.Amount += c
				}
			}
		}
		pots = append(pots, p)
		prev = lvl
	}

	// Final pot above highest all-in (uncapped overage)
	// Eligible = players with TotalBets > highest lvl (still not folded)
	over := NewPot(n)
	hasOver := false
	top := levels[len(levels)-1]
	for i := 0; i < n; i++ {
		tb := pm.TotalBets[i]
		if tb > top {
			over.Amount += (tb - top)
			if players[i] != nil && !players[i].HasFolded {
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

// DistributePots distributes all pots to the winners
// DistributePots distributes all pots to showdown winners.
// Robust to accidental calls on uncontested pots.
func (pm *PotManager) DistributePots(players []*Player) {
	if pm.log == nil {
		pm.log = slog.NewBackend(nil).Logger("TESTING")
	}
	for pi, pot := range pm.Pots {
		// collect eligible + not-folded
		var alive []int
		if len(pot.Eligibility) != len(players) {
			pm.log.Errorf("[pot %d] eligibility len %d != players len %d (index drift?)",
				pi, len(pot.Eligibility), len(players))
		}
		for idx, elig := range pot.Eligibility {
			if idx < 0 || idx >= len(players) {
				pm.log.Errorf("[pot %d] eligibility idx %d out of range (players=%d)", pi, idx, len(players))
				continue
			}
			if elig && players[idx] != nil && !players[idx].HasFolded {
				alive = append(alive, idx)
			}
		}

		// Defensive: if exactly one alive, pay them (uncontested pot leaked here)
		if len(alive) == 1 {
			w := alive[0]
			before := players[w].Balance
			players[w].Balance += pot.Amount
			pm.log.Infof("[pot %d] fold-win fallback -> P%d gets %d (bal %d -> %d)",
				pi, w, pot.Amount, before, players[w].Balance)
			continue
		}

		if len(alive) == 0 {
			pm.log.Errorf("[pot %d] no eligible alive players; pot=%d (this should never happen)", pi, pot.Amount)
			continue
		}

		// Showdown path: everyone alive must have HandValue.
		var winners []int
		var best *HandValue
		missing := 0

		for _, idx := range alive {
			hv := players[idx].HandValue
			if hv == nil {
				missing++
				pm.log.Errorf("[pot %d] player %d eligible at showdown but HandValue == nil", pi, idx)
				continue
			}
			if best == nil || CompareHands(*hv, *best) > 0 {
				best = hv
				winners = []int{idx}
			} else if CompareHands(*hv, *best) == 0 {
				winners = append(winners, idx)
			}
		}

		if missing > 0 && len(winners) == 0 {
			pm.log.Errorf("[pot %d] %d/%d alive players missing HandValue; cannot settle. Upstream must compute before distribution.",
				pi, missing, len(alive))
			continue
		}

		if len(winners) == 0 {
			pm.log.Errorf("[pot %d] showdown produced no winners (logic bug).", pi)
			continue
		}

		share := pot.Amount / int64(len(winners))
		rem := pot.Amount % int64(len(winners))
		for i, idx := range winners {
			add := share
			if i == 0 && rem > 0 {
				add += rem
			}
			// before := players[idx].Balance
			players[idx].Balance += add
			// pm.log.Infof("[pot %d] winner P%d gets %d (bal %d -> %d)", pi, idx, add, before, players[idx].Balance)
		}
	}
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
	}
}

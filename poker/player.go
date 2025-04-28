package poker

import (
	"fmt"
	"time"
)

// Player represents a poker player
type Player struct {
	ID              string
	Name            string
	Balance         int64
	TableSeat       int
	Hand            []Card
	HasFolded       bool
	HasBet          int64
	LastAction      time.Time
	IsReady         bool
	IsAllIn         bool
	IsDealer        bool
	IsTurn          bool
	HandValue       *HandValue
	HandDescription string
}

// NewPlayer creates a new player
func NewPlayer(id, name string, balance int64) *Player {
	return &Player{
		ID:         id,
		Name:       name,
		Balance:    balance,
		TableSeat:  -1,
		Hand:       make([]Card, 0, 2),
		HasFolded:  false,
		HasBet:     0,
		LastAction: time.Now(),
		IsReady:    false,
		IsAllIn:    false,
		IsDealer:   false,
		IsTurn:     false,
	}
}

// Bet places a bet for the player
func (p *Player) Bet(amount int64) error {
	if amount > p.Balance {
		return fmt.Errorf("insufficient balance")
	}

	p.Balance -= amount
	p.HasBet += amount
	p.LastAction = time.Now()
	return nil
}

// Fold makes the player fold their hand
func (p *Player) Fold() {
	p.HasFolded = true
	p.LastAction = time.Now()
}

// Reset resets the player's hand and betting state
func (p *Player) Reset() {
	p.Hand = p.Hand[:0]
	p.HasFolded = false
	p.HasBet = 0
	p.LastAction = time.Now()
}

// GetHandString returns a string representation of the player's hand
func (p *Player) GetHandString() string {
	if len(p.Hand) == 0 {
		return "No cards"
	}

	str := ""
	for i, card := range p.Hand {
		if i > 0 {
			str += " "
		}
		str += card.String()
	}
	return str
}

// GetStatus returns a string representation of the player's status
func (p *Player) GetStatus() string {
	status := fmt.Sprintf("Player %s:\n", p.Name)
	status += fmt.Sprintf("Balance: %.8f DCR\n", float64(p.Balance)/1e8)
	status += fmt.Sprintf("Current Bet: %.8f DCR\n", float64(p.HasBet)/1e8)
	status += fmt.Sprintf("Hand: %s\n", p.GetHandString())
	if p.HasFolded {
		status += "Status: Folded\n"
	} else {
		status += "Status: Active\n"
	}
	return status
}

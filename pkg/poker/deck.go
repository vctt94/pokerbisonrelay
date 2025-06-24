package poker

import (
	"math/rand"
)

// Suit represents a card suit
type Suit string

const (
	Spades   Suit = "♠"
	Hearts   Suit = "♥"
	Diamonds Suit = "♦"
	Clubs    Suit = "♣"
)

// Value represents a card value
type Value string

const (
	Ace   Value = "A"
	Two   Value = "2"
	Three Value = "3"
	Four  Value = "4"
	Five  Value = "5"
	Six   Value = "6"
	Seven Value = "7"
	Eight Value = "8"
	Nine  Value = "9"
	Ten   Value = "10"
	Jack  Value = "J"
	Queen Value = "Q"
	King  Value = "K"
)

// Card represents a playing card
type Card struct {
	suit  Suit
	value Value
}

// String returns a string representation of the card
func (c Card) String() string {
	return string(c.value) + string(c.suit)
}

// GetSuit returns the card's suit
func (c Card) GetSuit() string {
	return string(c.suit)
}

// GetValue returns the card's value
func (c Card) GetValue() string {
	return string(c.value)
}

// Deck represents a deck of cards
type Deck struct {
	cards []Card
	rng   *rand.Rand
}

// NewDeck creates a new deck of cards with the given random number generator
func NewDeck(rng *rand.Rand) *Deck {
	deck := &Deck{
		cards: make([]Card, 0, 52),
		rng:   rng,
	}

	// Create all 52 cards
	suits := []Suit{Spades, Hearts, Diamonds, Clubs}
	values := []Value{Ace, Two, Three, Four, Five, Six, Seven, Eight, Nine, Ten, Jack, Queen, King}

	for _, suit := range suits {
		for _, value := range values {
			deck.cards = append(deck.cards, Card{suit: suit, value: value})
		}
	}

	// Shuffle the deck
	deck.Shuffle()

	return deck
}

// Shuffle randomizes the order of cards in the deck
func (d *Deck) Shuffle() {
	d.rng.Shuffle(len(d.cards), func(i, j int) {
		d.cards[i], d.cards[j] = d.cards[j], d.cards[i]
	})
}

// Draw removes and returns the top card from the deck
func (d *Deck) Draw() (Card, bool) {
	if len(d.cards) == 0 {
		return Card{}, false
	}
	card := d.cards[0]
	d.cards = d.cards[1:]
	return card, true
}

// Size returns the number of cards remaining in the deck
func (d *Deck) Size() int {
	return len(d.cards)
}

// NewCardFromSuitValue creates a new Card with the given suit and value
// This is needed because Card fields are unexported
func NewCardFromSuitValue(suit Suit, value Value) Card {
	return Card{suit: suit, value: value}
}

// initializeDeck creates a new deck of cards
func initializeDeck() []Card {
	// Create a new deck with a deterministic seed for testing
	rng := rand.New(rand.NewSource(42))
	deck := NewDeck(rng)

	// Convert to slice for compatibility with existing code
	cards := make([]Card, len(deck.cards))
	copy(cards, deck.cards)
	return cards
}

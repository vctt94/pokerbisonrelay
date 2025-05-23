package poker

import (
	"math/rand"
	"testing"
)

func TestNewDeck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	deck := NewDeck(rng)

	// Check deck size
	if deck.Size() != 52 {
		t.Errorf("Expected deck size 52, got %d", deck.Size())
	}

	// Check that all cards are unique
	seen := make(map[Card]bool)
	for _, card := range deck.cards {
		if seen[card] {
			t.Errorf("Duplicate card found: %v", card)
		}
		seen[card] = true
	}

	// Check that all suits and values are present
	suitCount := make(map[Suit]int)
	valueCount := make(map[Value]int)
	for _, card := range deck.cards {
		suitCount[card.suit]++
		valueCount[card.value]++
	}

	// Check suit distribution
	for suit, count := range suitCount {
		if count != 13 {
			t.Errorf("Expected 13 cards of suit %v, got %d", suit, count)
		}
	}

	// Check value distribution
	for value, count := range valueCount {
		if count != 4 {
			t.Errorf("Expected 4 cards of value %v, got %d", value, count)
		}
	}
}

func TestDeckShuffle(t *testing.T) {
	// Create two decks with the same seed
	rng1 := rand.New(rand.NewSource(42))
	rng2 := rand.New(rand.NewSource(42))
	deck1 := NewDeck(rng1)
	deck2 := NewDeck(rng2)

	// Both decks should have the same order
	for i := 0; i < 52; i++ {
		if deck1.cards[i] != deck2.cards[i] {
			t.Errorf("Decks with same seed should have same order at position %d", i)
		}
	}

	// Create a deck with a different seed
	rng3 := rand.New(rand.NewSource(43))
	deck3 := NewDeck(rng3)

	// This deck should have a different order
	sameOrder := true
	for i := 0; i < 52; i++ {
		if deck1.cards[i] != deck3.cards[i] {
			sameOrder = false
			break
		}
	}
	if sameOrder {
		t.Error("Decks with different seeds should have different orders")
	}
}

func TestDeckDraw(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	deck := NewDeck(rng)

	// Draw all cards
	for i := 0; i < 52; i++ {
		card, ok := deck.Draw()
		if !ok {
			t.Errorf("Expected to draw card %d, but deck was empty", i)
		}
		if deck.Size() != 51-i {
			t.Errorf("Expected deck size %d after drawing, got %d", 51-i, deck.Size())
		}
		// Verify the card is valid
		if card.suit == "" || card.value == "" {
			t.Errorf("Drawn card %d is invalid: %v", i, card)
		}
	}

	// Try to draw from empty deck
	card, ok := deck.Draw()
	if ok {
		t.Error("Expected to fail drawing from empty deck")
	}
	if card != (Card{}) {
		t.Error("Expected zero value card when drawing from empty deck")
	}
}

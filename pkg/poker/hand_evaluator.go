package poker

import (
	"fmt"
	"sort"

	"github.com/chehsunliu/poker"
	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
)

// HandRank represents the rank of a poker hand
type HandRank int

const (
	HighCard HandRank = iota
	Pair
	TwoPair
	ThreeOfAKind
	Straight
	Flush
	FullHouse
	FourOfAKind
	StraightFlush
	RoyalFlush
)

// HandValue represents a complete evaluation of a hand, including rank and kickers
type HandValue struct {
	Rank            HandRank
	RankValue       int    // Value of the primary cards (pair, trips, etc.)
	Kickers         []int  // Values of kicker cards in descending order
	BestHand        []Card // The 5 cards that make up the best hand
	HandRank        pokerrpc.HandRank
	HandDescription string
}

// valueToInt converts a card Value to its integer representation
func valueToInt(value Value) int {
	switch value {
	case Ace:
		return 14
	case King:
		return 13
	case Queen:
		return 12
	case Jack:
		return 11
	case Ten:
		return 10
	case Nine:
		return 9
	case Eight:
		return 8
	case Seven:
		return 7
	case Six:
		return 6
	case Five:
		return 5
	case Four:
		return 4
	case Three:
		return 3
	case Two:
		return 2
	default:
		return 0
	}
}

// intToValue converts an integer to its card Value representation
func intToValue(value int) Value {
	switch value {
	case 14:
		return Ace
	case 13:
		return King
	case 12:
		return Queen
	case 11:
		return Jack
	case 10:
		return Ten
	case 9:
		return Nine
	case 8:
		return Eight
	case 7:
		return Seven
	case 6:
		return Six
	case 5:
		return Five
	case 4:
		return Four
	case 3:
		return Three
	case 2:
		return Two
	default:
		return ""
	}
}

// convertCardToChehsunliu converts our Card type to the chehsunliu/poker Card type.
// Returns an error if the rank or suit is invalid instead of silently defaulting.
func convertCardToChehsunliu(card Card) (poker.Card, error) {
	// Rank
	var rankChar byte
	switch Value(card.GetValue()) {
	case Two:
		rankChar = '2'
	case Three:
		rankChar = '3'
	case Four:
		rankChar = '4'
	case Five:
		rankChar = '5'
	case Six:
		rankChar = '6'
	case Seven:
		rankChar = '7'
	case Eight:
		rankChar = '8'
	case Nine:
		rankChar = '9'
	case Ten:
		rankChar = 'T'
	case Jack:
		rankChar = 'J'
	case Queen:
		rankChar = 'Q'
	case King:
		rankChar = 'K'
	case Ace:
		rankChar = 'A'
	default:
		var emptyCard poker.Card
		return emptyCard, fmt.Errorf("invalid rank: %v", card.GetValue())
	}

	// Suit
	var suitChar byte
	switch Suit(card.GetSuit()) {
	case Spades:
		suitChar = 's'
	case Hearts:
		suitChar = 'h'
	case Diamonds:
		suitChar = 'd'
	case Clubs:
		suitChar = 'c'
	default:
		var emptyCard poker.Card
		return emptyCard, fmt.Errorf("invalid suit: %v", card.GetSuit())
	}

	cs := string([]byte{rankChar, suitChar})
	return poker.NewCard(cs), nil
}

// convertRankClassToHandRank converts chehsunliu rank class to our HandRank
func convertRankClassToHandRank(rankClass int32) HandRank {
	switch rankClass {
	case 1: // Straight flush
		return StraightFlush
	case 2: // Four of a kind
		return FourOfAKind
	case 3: // Full house
		return FullHouse
	case 4: // Flush
		return Flush
	case 5: // Straight
		return Straight
	case 6: // Three of a kind
		return ThreeOfAKind
	case 7: // Two pair
		return TwoPair
	case 8: // Pair
		return Pair
	case 9: // High card
		return HighCard
	default:
		return HighCard
	}
}

// convertRankClassToGRPCHandRank converts chehsunliu rank class to gRPC HandRank
func convertRankClassToGRPCHandRank(rankClass int32) pokerrpc.HandRank {
	switch rankClass {
	case 1: // Straight flush
		return pokerrpc.HandRank_STRAIGHT_FLUSH
	case 2: // Four of a kind
		return pokerrpc.HandRank_FOUR_OF_A_KIND
	case 3: // Full house
		return pokerrpc.HandRank_FULL_HOUSE
	case 4: // Flush
		return pokerrpc.HandRank_FLUSH
	case 5: // Straight
		return pokerrpc.HandRank_STRAIGHT
	case 6: // Three of a kind
		return pokerrpc.HandRank_THREE_OF_A_KIND
	case 7: // Two pair
		return pokerrpc.HandRank_TWO_PAIR
	case 8: // Pair
		return pokerrpc.HandRank_PAIR
	case 9: // High card
		return pokerrpc.HandRank_HIGH_CARD
	default:
		return pokerrpc.HandRank_HIGH_CARD
	}
}

// EvaluateHand evaluates a player's best 5-card hand from their 2 hole cards and the 5 community cards
func EvaluateHand(holeCards []Card, communityCards []Card) (HandValue, error) {
	// Combine hole cards and community cards
	allCards := append([]Card{}, holeCards...)
	allCards = append(allCards, communityCards...)

	// Convert to chehsunliu format
	chehsunliuCards := make([]poker.Card, 0, len(allCards))
	for _, card := range allCards {
		convertedCard, err := convertCardToChehsunliu(card)
		if err != nil {
			return HandValue{}, fmt.Errorf("failed to convert card: %w", err)
		}
		chehsunliuCards = append(chehsunliuCards, convertedCard)
	}

	// Evaluate using chehsunliu library
	rank := poker.Evaluate(chehsunliuCards)
	rankClass := poker.RankClass(rank)
	rankString := poker.RankString(rank)

	// Get best 5 cards
	bestCards, err := getBestFiveCards(allCards)
	if err != nil {
		return HandValue{}, fmt.Errorf("failed to get best five cards: %w", err)
	}

	// Create HandValue with chehsunliu results
	handValue := HandValue{
		Rank:            convertRankClassToHandRank(rankClass),
		RankValue:       int(rank), // Use the actual rank value for comparison
		Kickers:         []int{},   // Simplified - chehsunliu handles this internally
		BestHand:        bestCards, // Get best 5 cards
		HandRank:        convertRankClassToGRPCHandRank(rankClass),
		HandDescription: rankString,
	}

	return handValue, nil
}

// getBestFiveCards returns the best 5 cards from a hand using chehsunliu evaluation
func getBestFiveCards(cards []Card) ([]Card, error) {
	if len(cards) <= 5 {
		// If we have 5 or fewer cards, return them all
		return cards, nil
	}

	// Convert all cards to chehsunliu format
	chehsunliuCards := make([]poker.Card, 0, len(cards))
	for _, card := range cards {
		convertedCard, err := convertCardToChehsunliu(card)
		if err != nil {
			return nil, fmt.Errorf("failed to convert card: %w", err)
		}
		chehsunliuCards = append(chehsunliuCards, convertedCard)
	}

	// Use chehsunliu to find the best 5-card combination
	// Since chehsunliu.Evaluate takes all cards and finds the best 5,
	// we can use it to determine which 5 cards form the best hand
	bestRank := poker.Evaluate(chehsunliuCards)

	// For 6 or 7 cards, we need to try all combinations to find which 5 cards
	// produce the best rank that matches our evaluation
	bestCards := make([]Card, 0, 5)

	// Generate all possible 5-card combinations and find the one that matches our best rank
	combinations := generateCombinations(cards, 5)
	for _, combo := range combinations {
		// Convert this combination to chehsunliu format
		comboChehsunliu := make([]poker.Card, 0, 5)
		for _, card := range combo {
			convertedCard, err := convertCardToChehsunliu(card)
			if err != nil {
				return nil, fmt.Errorf("failed to convert card in combination: %w", err)
			}
			comboChehsunliu = append(comboChehsunliu, convertedCard)
		}

		// Check if this combination produces the same rank as our best
		if poker.Evaluate(comboChehsunliu) == bestRank {
			bestCards = combo
			break
		}
	}

	// If we couldn't find the exact match (shouldn't happen), fall back to sorted cards
	if len(bestCards) == 0 {
		sortedCards := make([]Card, len(cards))
		copy(sortedCards, cards)
		sortCardsByValue(sortedCards)
		bestCards = sortedCards[:5]
	}

	return bestCards, nil
}

// generateCombinations generates all possible k-combinations from a slice of cards
func generateCombinations(cards []Card, k int) [][]Card {
	var combinations [][]Card

	if k > len(cards) || k <= 0 {
		return combinations
	}

	if k == len(cards) {
		return [][]Card{cards}
	}

	// Generate combinations recursively
	var generate func(start int, current []Card)
	generate = func(start int, current []Card) {
		if len(current) == k {
			combination := make([]Card, k)
			copy(combination, current)
			combinations = append(combinations, combination)
			return
		}

		for i := start; i <= len(cards)-(k-len(current)); i++ {
			generate(i+1, append(current, cards[i]))
		}
	}

	generate(0, []Card{})
	return combinations
}

// Helper function to sort cards by value (highest first)
func sortCardsByValue(cards []Card) {
	sort.Slice(cards, func(i, j int) bool {
		return valueToInt(cards[i].value) > valueToInt(cards[j].value)
	})
}

// Helper function to check if a card is already in a slice
func cardInSlice(card Card, cards []Card) bool {
	for _, c := range cards {
		if c.value == card.value && c.suit == card.suit {
			return true
		}
	}
	return false
}

// GetHandDescription returns a human-readable description of a hand
func GetHandDescription(handValue HandValue) string {
	return handValue.HandDescription
}

// CompareHands compares two hand values and returns:
// -1 if handA < handB (handA is worse)
// 0 if handA == handB (tie)
// 1 if handA > handB (handA is better)
// Note: In chehsunliu library, lower rank values are better
func CompareHands(handA, handB HandValue) int {
	// In chehsunliu library, lower values are better
	// So we need to reverse the comparison
	if handA.RankValue > handB.RankValue {
		return -1 // handA is worse (higher rank value)
	}
	if handA.RankValue < handB.RankValue {
		return 1 // handA is better (lower rank value)
	}

	// If rank values are the same, it's a tie
	// (chehsunliu handles all tiebreakers internally in the rank value)
	return 0
}

// CreateHandFromCards creates a Card slice from a slice of Card objects for gRPC
func CreateHandFromCards(cards []Card) []*pokerrpc.Card {
	pbCards := make([]*pokerrpc.Card, len(cards))
	for i, card := range cards {
		pbCards[i] = &pokerrpc.Card{
			Suit:  card.GetSuit(),
			Value: card.GetValue(),
		}
	}

	return pbCards
}

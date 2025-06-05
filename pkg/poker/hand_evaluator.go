package poker

import (
	"sort"

	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
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
	Rank      HandRank
	RankValue int    // Value of the primary cards (pair, trips, etc.)
	Kickers   []int  // Values of kicker cards in descending order
	BestHand  []Card // The 5 cards that make up the best hand
	HandRank  pokerrpc.HandRank
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

// EvaluateHand evaluates a player's best 5-card hand from their 2 hole cards and the 5 community cards
func EvaluateHand(holeCards []Card, communityCards []Card) HandValue {
	// Combine hole cards and community cards
	cards := append([]Card{}, holeCards...)
	cards = append(cards, communityCards...)

	// Get all 5-card combinations from the 7 cards
	handValue := findBestHand(cards)

	// Convert internal HandRank to gRPC HandRank
	handValue.HandRank = convertToGRPCHandRank(handValue.Rank)

	return handValue
}

// findBestHand finds the best 5-card hand from the given cards
func findBestHand(cards []Card) HandValue {
	// Convert cards to a more convenient format
	values := make([]int, len(cards))
	suits := make([]string, len(cards))

	for i, card := range cards {
		values[i] = valueToInt(card.value)
		suits[i] = string(card.suit)
	}

	// Check for each hand type, from best to worst
	if handValue, ok := checkRoyalFlush(cards, values, suits); ok {
		return handValue
	}
	if handValue, ok := checkStraightFlush(cards, values, suits); ok {
		return handValue
	}
	if handValue, ok := checkFourOfAKind(cards, values); ok {
		return handValue
	}
	if handValue, ok := checkFullHouse(cards, values); ok {
		return handValue
	}
	if handValue, ok := checkFlush(cards, suits); ok {
		return handValue
	}
	if handValue, ok := checkStraight(cards, values); ok {
		return handValue
	}
	if handValue, ok := checkThreeOfAKind(cards, values); ok {
		return handValue
	}
	if handValue, ok := checkTwoPair(cards, values); ok {
		return handValue
	}
	if handValue, ok := checkPair(cards, values); ok {
		return handValue
	}

	// If no other hand type is found, it's a high card
	return checkHighCard(cards, values)
}

// checkRoyalFlush checks for a royal flush
func checkRoyalFlush(cards []Card, values []int, suits []string) (HandValue, bool) {
	// A royal flush is a straight flush with Ace high
	if handValue, ok := checkStraightFlush(cards, values, suits); ok {
		if handValue.RankValue == 14 { // Ace high
			return HandValue{
				Rank:      RoyalFlush,
				RankValue: 14,
				Kickers:   []int{},
				BestHand:  handValue.BestHand,
			}, true
		}
	}
	return HandValue{}, false
}

// checkStraightFlush checks for a straight flush
func checkStraightFlush(cards []Card, values []int, suits []string) (HandValue, bool) {
	// Group cards by suit
	suitGroups := make(map[string][]Card)
	for i, card := range cards {
		suit := suits[i]
		suitGroups[suit] = append(suitGroups[suit], card)
	}

	// Look for a suit with at least 5 cards
	for _, suitedCards := range suitGroups {
		if len(suitedCards) >= 5 {
			// Check for a straight within this suit
			sortCardsByValue(suitedCards)
			if handValue, ok := findStraight(suitedCards); ok {
				return HandValue{
					Rank:      StraightFlush,
					RankValue: handValue.RankValue,
					Kickers:   []int{},
					BestHand:  handValue.BestHand,
				}, true
			}
		}
	}

	return HandValue{}, false
}

// checkFourOfAKind checks for four of a kind
func checkFourOfAKind(cards []Card, values []int) (HandValue, bool) {
	valueCount := make(map[int]int)
	for _, value := range values {
		valueCount[value]++
	}

	var fourOfAKindValue int
	var kicker int

	// Find the value that appears 4 times
	for value, count := range valueCount {
		if count == 4 {
			fourOfAKindValue = value
		}
	}

	if fourOfAKindValue == 0 {
		return HandValue{}, false
	}

	// Find the highest card that isn't part of the four of a kind
	sortedValues := make([]int, 0, len(values))
	for value := range valueCount {
		if value != fourOfAKindValue {
			sortedValues = append(sortedValues, value)
		}
	}

	sort.Slice(sortedValues, func(i, j int) bool {
		return sortedValues[i] > sortedValues[j]
	})

	if len(sortedValues) > 0 {
		kicker = sortedValues[0]
	}

	// Build the best hand
	bestHand := make([]Card, 0, 5)

	// Add the four cards of the same value
	for _, card := range cards {
		if valueToInt(card.value) == fourOfAKindValue && len(bestHand) < 4 {
			bestHand = append(bestHand, card)
		}
	}

	// Add the kicker
	for _, card := range cards {
		if valueToInt(card.value) == kicker && len(bestHand) < 5 {
			bestHand = append(bestHand, card)
			break
		}
	}

	return HandValue{
		Rank:      FourOfAKind,
		RankValue: fourOfAKindValue,
		Kickers:   []int{kicker},
		BestHand:  bestHand,
	}, true
}

// checkFullHouse checks for a full house
func checkFullHouse(cards []Card, values []int) (HandValue, bool) {
	valueCount := make(map[int]int)
	for _, value := range values {
		valueCount[value]++
	}

	var threeOfAKindValue int
	var pairValue int

	// Find values that appear 3 and 2 times
	for value, count := range valueCount {
		if count >= 3 {
			if threeOfAKindValue == 0 || value > threeOfAKindValue {
				// If we already had a three of a kind, demote it to a pair if it's higher
				if threeOfAKindValue > 0 && threeOfAKindValue > pairValue {
					pairValue = threeOfAKindValue
				}
				threeOfAKindValue = value
			} else if pairValue == 0 || value > pairValue {
				pairValue = value
			}
		} else if count >= 2 {
			if pairValue == 0 || value > pairValue {
				pairValue = value
			}
		}
	}

	if threeOfAKindValue == 0 || pairValue == 0 {
		return HandValue{}, false
	}

	// Build the best hand
	bestHand := make([]Card, 0, 5)

	// Add the three cards of the same value
	for _, card := range cards {
		if valueToInt(card.value) == threeOfAKindValue && len(bestHand) < 3 {
			bestHand = append(bestHand, card)
		}
	}

	// Add the pair
	for _, card := range cards {
		if valueToInt(card.value) == pairValue && len(bestHand) < 5 {
			bestHand = append(bestHand, card)
		}
	}

	return HandValue{
		Rank:      FullHouse,
		RankValue: threeOfAKindValue,
		Kickers:   []int{pairValue},
		BestHand:  bestHand,
	}, true
}

// checkFlush checks for a flush
func checkFlush(cards []Card, suits []string) (HandValue, bool) {
	suitCount := make(map[string]int)
	suitCards := make(map[string][]Card)

	for i, suit := range suits {
		suitCount[suit]++
		suitCards[suit] = append(suitCards[suit], cards[i])
	}

	for suit, count := range suitCount {
		if count >= 5 {
			// Get the 5 highest cards of the suit
			flushCards := suitCards[suit]
			sortCardsByValue(flushCards)

			// Take the 5 highest cards
			bestHand := flushCards[:5]

			values := make([]int, 5)
			for i, card := range bestHand {
				values[i] = valueToInt(card.value)
			}

			return HandValue{
				Rank:      Flush,
				RankValue: values[0], // Highest card value
				Kickers:   values[1:],
				BestHand:  bestHand,
			}, true
		}
	}

	return HandValue{}, false
}

// checkStraight checks for a straight
func checkStraight(cards []Card, values []int) (HandValue, bool) {
	// Create a copy of the cards and sort by value
	sortedCards := make([]Card, len(cards))
	copy(sortedCards, cards)
	sortCardsByValue(sortedCards)

	if result, ok := findStraight(sortedCards); ok {
		return result, true
	}

	return HandValue{}, false
}

// findStraight helper to find a straight in a sorted set of cards
func findStraight(sortedCards []Card) (HandValue, bool) {
	// Get unique values in descending order
	uniqueValues := make([]int, 0)
	seen := make(map[int]bool)

	for _, card := range sortedCards {
		val := valueToInt(card.value)
		if !seen[val] {
			uniqueValues = append(uniqueValues, val)
			seen[val] = true
		}
	}

	sort.Slice(uniqueValues, func(i, j int) bool {
		return uniqueValues[i] > uniqueValues[j]
	})

	// Handle Ace-low straight (A-2-3-4-5)
	if seen[14] && seen[2] && seen[3] && seen[4] && seen[5] {
		// Create the straight hand
		bestHand := make([]Card, 0, 5)

		// Find the 5, 4, 3, 2 and Ace
		values := []int{5, 4, 3, 2, 14}
		for _, val := range values {
			for _, card := range sortedCards {
				if valueToInt(card.value) == val && !cardInSlice(card, bestHand) {
					bestHand = append(bestHand, card)
					break
				}
			}
		}

		return HandValue{
			Rank:      Straight,
			RankValue: 5, // The high card in an A-5 straight is 5
			Kickers:   []int{},
			BestHand:  bestHand,
		}, true
	}

	// Check for regular straights
	for i := 0; i <= len(uniqueValues)-5; i++ {
		if uniqueValues[i] == uniqueValues[i+4]+4 {
			// Create the straight hand
			bestHand := make([]Card, 0, 5)

			// Find the 5 cards that form the straight
			for j := 0; j < 5; j++ {
				targetValue := uniqueValues[i+j]
				for _, card := range sortedCards {
					if valueToInt(card.value) == targetValue && !cardInSlice(card, bestHand) {
						bestHand = append(bestHand, card)
						break
					}
				}
			}

			return HandValue{
				Rank:      Straight,
				RankValue: uniqueValues[i], // Highest card in the straight
				Kickers:   []int{},
				BestHand:  bestHand,
			}, true
		}
	}

	return HandValue{}, false
}

// checkThreeOfAKind checks for three of a kind
func checkThreeOfAKind(cards []Card, values []int) (HandValue, bool) {
	valueCount := make(map[int]int)
	for _, value := range values {
		valueCount[value]++
	}

	var threeOfAKindValue int

	// Find the value that appears 3 times
	for value, count := range valueCount {
		if count >= 3 {
			if threeOfAKindValue == 0 || value > threeOfAKindValue {
				threeOfAKindValue = value
			}
		}
	}

	if threeOfAKindValue == 0 {
		return HandValue{}, false
	}

	// Find the two highest cards that aren't part of the three of a kind
	kickers := make([]int, 0)
	for value := range valueCount {
		if value != threeOfAKindValue {
			kickers = append(kickers, value)
		}
	}

	// Sort kickers in descending order
	sort.Slice(kickers, func(i, j int) bool {
		return kickers[i] > kickers[j]
	})

	if len(kickers) > 2 {
		kickers = kickers[:2] // Keep only the top 2 kickers
	}

	// Build the best hand
	bestHand := make([]Card, 0, 5)

	// Add the three cards of the same value
	for _, card := range cards {
		if valueToInt(card.value) == threeOfAKindValue && len(bestHand) < 3 {
			bestHand = append(bestHand, card)
		}
	}

	// Add the kickers
	for _, kicker := range kickers {
		for _, card := range cards {
			if valueToInt(card.value) == kicker && !cardInSlice(card, bestHand) && len(bestHand) < 5 {
				bestHand = append(bestHand, card)
				break
			}
		}
	}

	return HandValue{
		Rank:      ThreeOfAKind,
		RankValue: threeOfAKindValue,
		Kickers:   kickers,
		BestHand:  bestHand,
	}, true
}

// checkTwoPair checks for two pairs
func checkTwoPair(cards []Card, values []int) (HandValue, bool) {
	valueCount := make(map[int]int)
	for _, value := range values {
		valueCount[value]++
	}

	pairs := make([]int, 0)

	// Find all values that appear at least twice
	for value, count := range valueCount {
		if count >= 2 {
			pairs = append(pairs, value)
		}
	}

	// Sort pairs in descending order
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i] > pairs[j]
	})

	if len(pairs) < 2 {
		return HandValue{}, false
	}

	// Take the two highest pairs
	highPair := pairs[0]
	lowPair := pairs[1]

	// Find the highest card that isn't part of either pair
	var kicker int
	for value := range valueCount {
		if value != highPair && value != lowPair && (kicker == 0 || value > kicker) {
			kicker = value
		}
	}

	// Build the best hand
	bestHand := make([]Card, 0, 5)

	// Add the high pair
	for _, card := range cards {
		if valueToInt(card.value) == highPair && len(bestHand) < 2 {
			bestHand = append(bestHand, card)
		}
	}

	// Add the low pair
	for _, card := range cards {
		if valueToInt(card.value) == lowPair && len(bestHand) < 4 {
			bestHand = append(bestHand, card)
		}
	}

	// Add the kicker
	for _, card := range cards {
		if valueToInt(card.value) == kicker && len(bestHand) < 5 {
			bestHand = append(bestHand, card)
			break
		}
	}

	return HandValue{
		Rank:      TwoPair,
		RankValue: highPair,
		Kickers:   []int{lowPair, kicker},
		BestHand:  bestHand,
	}, true
}

// checkPair checks for a pair
func checkPair(cards []Card, values []int) (HandValue, bool) {
	valueCount := make(map[int]int)
	for _, value := range values {
		valueCount[value]++
	}

	var pairValue int

	// Find the value that appears twice
	for value, count := range valueCount {
		if count >= 2 {
			if pairValue == 0 || value > pairValue {
				pairValue = value
			}
		}
	}

	if pairValue == 0 {
		return HandValue{}, false
	}

	// Find the three highest cards that aren't part of the pair
	kickers := make([]int, 0)
	for value := range valueCount {
		if value != pairValue {
			kickers = append(kickers, value)
		}
	}

	// Sort kickers in descending order
	sort.Slice(kickers, func(i, j int) bool {
		return kickers[i] > kickers[j]
	})

	if len(kickers) > 3 {
		kickers = kickers[:3] // Keep only the top 3 kickers
	}

	// Build the best hand
	bestHand := make([]Card, 0, 5)

	// Add the pair
	for _, card := range cards {
		if valueToInt(card.value) == pairValue && len(bestHand) < 2 {
			bestHand = append(bestHand, card)
		}
	}

	// Add the kickers
	for _, kicker := range kickers {
		for _, card := range cards {
			if valueToInt(card.value) == kicker && !cardInSlice(card, bestHand) && len(bestHand) < 5 {
				bestHand = append(bestHand, card)
				break
			}
		}
	}

	return HandValue{
		Rank:      Pair,
		RankValue: pairValue,
		Kickers:   kickers,
		BestHand:  bestHand,
	}, true
}

// checkHighCard determines the high card hand value
func checkHighCard(cards []Card, values []int) HandValue {
	// Create a copy of cards and sort by value
	sortedCards := make([]Card, len(cards))
	copy(sortedCards, cards)
	sortCardsByValue(sortedCards)

	// If we don't have enough cards, we can't properly evaluate
	if len(sortedCards) < 5 {
		// Return a minimal hand value for incomplete hands
		highValue := 0
		if len(sortedCards) > 0 {
			highValue = valueToInt(sortedCards[0].value)
		}

		// Create kickers from available cards (excluding the high card)
		kickers := make([]int, 0)
		for i := 1; i < len(sortedCards) && i < 5; i++ {
			kickers = append(kickers, valueToInt(sortedCards[i].value))
		}

		return HandValue{
			Rank:      HighCard,
			RankValue: highValue,
			Kickers:   kickers,
			BestHand:  sortedCards, // Use all available cards
		}
	}

	// Take the 5 highest cards
	bestHand := sortedCards[:5]

	// Extract the values for the hand value
	highValue := valueToInt(bestHand[0].value)
	kickers := make([]int, 4)
	for i := 0; i < 4; i++ {
		kickers[i] = valueToInt(bestHand[i+1].value)
	}

	return HandValue{
		Rank:      HighCard,
		RankValue: highValue,
		Kickers:   kickers,
		BestHand:  bestHand,
	}
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

// convertToGRPCHandRank converts the internal HandRank to the gRPC HandRank
func convertToGRPCHandRank(handRank HandRank) pokerrpc.HandRank {
	switch handRank {
	case HighCard:
		return pokerrpc.HandRank_HIGH_CARD
	case Pair:
		return pokerrpc.HandRank_PAIR
	case TwoPair:
		return pokerrpc.HandRank_TWO_PAIR
	case ThreeOfAKind:
		return pokerrpc.HandRank_THREE_OF_A_KIND
	case Straight:
		return pokerrpc.HandRank_STRAIGHT
	case Flush:
		return pokerrpc.HandRank_FLUSH
	case FullHouse:
		return pokerrpc.HandRank_FULL_HOUSE
	case FourOfAKind:
		return pokerrpc.HandRank_FOUR_OF_A_KIND
	case StraightFlush:
		return pokerrpc.HandRank_STRAIGHT_FLUSH
	case RoyalFlush:
		return pokerrpc.HandRank_ROYAL_FLUSH
	default:
		return pokerrpc.HandRank_HIGH_CARD
	}
}

// GetHandDescription returns a human-readable description of a hand
func GetHandDescription(handValue HandValue) string {
	switch handValue.Rank {
	case RoyalFlush:
		return "Royal Flush"
	case StraightFlush:
		highCard := intToValue(handValue.RankValue)
		return "Straight Flush, " + string(highCard) + " high"
	case FourOfAKind:
		value := intToValue(handValue.RankValue)
		return "Four of a Kind, " + string(value) + "s"
	case FullHouse:
		threeKind := intToValue(handValue.RankValue)
		pair := intToValue(handValue.Kickers[0])
		return "Full House, " + string(threeKind) + "s over " + string(pair) + "s"
	case Flush:
		suit := handValue.BestHand[0].suit
		return "Flush, " + string(suit)
	case Straight:
		highCard := intToValue(handValue.RankValue)
		return "Straight, " + string(highCard) + " high"
	case ThreeOfAKind:
		value := intToValue(handValue.RankValue)
		return "Three of a Kind, " + string(value) + "s"
	case TwoPair:
		highPair := intToValue(handValue.RankValue)
		lowPair := intToValue(handValue.Kickers[0])
		return "Two Pair, " + string(highPair) + "s and " + string(lowPair) + "s"
	case Pair:
		value := intToValue(handValue.RankValue)
		return "Pair of " + string(value) + "s"
	case HighCard:
		value := intToValue(handValue.RankValue)
		return "High Card " + string(value)
	default:
		return "Unknown Hand"
	}
}

// CompareHands compares two hand values and returns:
// -1 if handA < handB
// 0 if handA == handB
// 1 if handA > handB
func CompareHands(handA, handB HandValue) int {
	// First compare hand ranks
	if handA.Rank < handB.Rank {
		return -1
	}
	if handA.Rank > handB.Rank {
		return 1
	}

	// If ranks are the same, compare the rank value
	if handA.RankValue < handB.RankValue {
		return -1
	}
	if handA.RankValue > handB.RankValue {
		return 1
	}

	// If rank values are the same, compare kickers in order
	for i := 0; i < len(handA.Kickers) && i < len(handB.Kickers); i++ {
		if handA.Kickers[i] < handB.Kickers[i] {
			return -1
		}
		if handA.Kickers[i] > handB.Kickers[i] {
			return 1
		}
	}

	// If everything is the same, it's a tie
	return 0
}

// CreateHandFromCards creates a Card slice from a slice of Card objects for gRPC
func CreateHandFromCards(cards []Card) []*pokerrpc.Card {
	pbCards := make([]*pokerrpc.Card, len(cards))
	for i, card := range cards {
		pbCards[i] = &pokerrpc.Card{
			Suit:  string(card.suit),
			Value: string(card.value),
		}
	}

	return pbCards
}

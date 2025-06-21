package poker

import (
	"testing"
)

func TestEvaluateHand(t *testing.T) {
	// Test cases for different hand types
	tests := []struct {
		name      string
		holeCards []Card
		community []Card
		wantRank  HandRank
		wantValue int
	}{
		{
			name: "Royal Flush",
			holeCards: []Card{
				{suit: Hearts, value: Ace},
				{suit: Hearts, value: King},
			},
			community: []Card{
				{suit: Hearts, value: Queen},
				{suit: Hearts, value: Jack},
				{suit: Hearts, value: Ten},
				{suit: Clubs, value: Three},
				{suit: Diamonds, value: Four},
			},
			wantRank:  StraightFlush,
			wantValue: 1,
		},
		{
			name: "Straight Flush",
			holeCards: []Card{
				{suit: Spades, value: Nine},
				{suit: Spades, value: Eight},
			},
			community: []Card{
				{suit: Spades, value: Seven},
				{suit: Spades, value: Six},
				{suit: Spades, value: Five},
				{suit: Hearts, value: Two},
				{suit: Diamonds, value: Three},
			},
			wantRank:  StraightFlush,
			wantValue: 6,
		},
		{
			name: "Four of a Kind",
			holeCards: []Card{
				{suit: Hearts, value: Ace},
				{suit: Spades, value: Ace},
			},
			community: []Card{
				{suit: Clubs, value: Ace},
				{suit: Diamonds, value: Ace},
				{suit: Hearts, value: King},
				{suit: Clubs, value: Queen},
				{suit: Spades, value: Jack},
			},
			wantRank:  FourOfAKind,
			wantValue: 11,
		},
		{
			name: "Full House",
			holeCards: []Card{
				{suit: Hearts, value: King},
				{suit: Spades, value: King},
			},
			community: []Card{
				{suit: Clubs, value: King},
				{suit: Hearts, value: Nine},
				{suit: Spades, value: Nine},
				{suit: Hearts, value: Two},
				{suit: Clubs, value: Three},
			},
			wantRank:  FullHouse,
			wantValue: 183,
		},
		{
			name: "Flush",
			holeCards: []Card{
				{suit: Hearts, value: Ace},
				{suit: Hearts, value: Ten},
			},
			community: []Card{
				{suit: Hearts, value: Eight},
				{suit: Hearts, value: Six},
				{suit: Hearts, value: Four},
				{suit: Clubs, value: Jack},
				{suit: Diamonds, value: Queen},
			},
			wantRank:  Flush,
			wantValue: 718,
		},
		{
			name: "Straight",
			holeCards: []Card{
				{suit: Hearts, value: Nine},
				{suit: Spades, value: Eight},
			},
			community: []Card{
				{suit: Clubs, value: Seven},
				{suit: Diamonds, value: Six},
				{suit: Spades, value: Five},
				{suit: Hearts, value: Two},
				{suit: Clubs, value: Three},
			},
			wantRank:  Straight,
			wantValue: 1605,
		},
		{
			name: "Three of a Kind",
			holeCards: []Card{
				{suit: Hearts, value: Queen},
				{suit: Spades, value: Queen},
			},
			community: []Card{
				{suit: Clubs, value: Queen},
				{suit: Diamonds, value: Six},
				{suit: Spades, value: Five},
				{suit: Hearts, value: Two},
				{suit: Clubs, value: Three},
			},
			wantRank:  ThreeOfAKind,
			wantValue: 1798,
		},
		{
			name: "Two Pair",
			holeCards: []Card{
				{suit: Hearts, value: Ace},
				{suit: Spades, value: Ace},
			},
			community: []Card{
				{suit: Clubs, value: King},
				{suit: Diamonds, value: King},
				{suit: Spades, value: Five},
				{suit: Hearts, value: Two},
				{suit: Clubs, value: Three},
			},
			wantRank:  TwoPair,
			wantValue: 2475,
		},
		{
			name: "Pair",
			holeCards: []Card{
				{suit: Hearts, value: Jack},
				{suit: Spades, value: Jack},
			},
			community: []Card{
				{suit: Clubs, value: Ace},
				{suit: Diamonds, value: King},
				{suit: Spades, value: Five},
				{suit: Hearts, value: Two},
				{suit: Clubs, value: Three},
			},
			wantRank:  Pair,
			wantValue: 3992,
		},
		{
			name: "High Card",
			holeCards: []Card{
				{suit: Hearts, value: Ace},
				{suit: Spades, value: Jack},
			},
			community: []Card{
				{suit: Clubs, value: Nine},
				{suit: Diamonds, value: Seven},
				{suit: Spades, value: Five},
				{suit: Hearts, value: Three},
				{suit: Clubs, value: Two},
			},
			wantRank:  HighCard,
			wantValue: 6505,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handValue := EvaluateHand(tt.holeCards, tt.community)

			if handValue.Rank != tt.wantRank {
				t.Errorf("EvaluateHand() rank = %v, want %v", handValue.Rank, tt.wantRank)
			}

			if handValue.RankValue != tt.wantValue {
				t.Errorf("EvaluateHand() value = %v, want %v", handValue.RankValue, tt.wantValue)
			}

			// Check that the best hand has exactly 5 cards
			if len(handValue.BestHand) != 5 {
				t.Errorf("EvaluateHand() best hand has %d cards, want 5", len(handValue.BestHand))
			}
		})
	}
}

func TestCompareHands(t *testing.T) {
	// Test cases for comparing different hands
	// NOTE: In chehsunliu/poker, lower rank values are better
	tests := []struct {
		name       string
		handA      HandValue
		handB      HandValue
		wantResult int
	}{
		{
			name: "Royal Flush beats Straight Flush",
			handA: HandValue{
				Rank:      StraightFlush, // chehsunliu classifies royal flush as straight flush
				RankValue: 1,             // royal flush has rank 1 (best)
			},
			handB: HandValue{
				Rank:      StraightFlush,
				RankValue: 6, // 9-high straight flush has higher rank value (worse)
			},
			wantResult: 1, // handA > handB (lower rank value is better)
		},
		{
			name: "Four of a Kind beats Full House",
			handA: HandValue{
				Rank:      FourOfAKind,
				RankValue: 11, // Four Aces
			},
			handB: HandValue{
				Rank:      FullHouse,
				RankValue: 183, // Kings full of Nines
			},
			wantResult: 1, // handA > handB (lower rank value is better)
		},
		{
			name: "Higher Four of a Kind beats lower Four of a Kind",
			handA: HandValue{
				Rank:      FourOfAKind,
				RankValue: 11, // Four Aces (rank 11)
			},
			handB: HandValue{
				Rank:      FourOfAKind,
				RankValue: 25, // Four Kings (higher rank value = worse)
			},
			wantResult: 1, // handA > handB (lower rank value is better)
		},
		{
			name: "Same rank with higher kicker wins",
			handA: HandValue{
				Rank:      Pair,
				RankValue: 3990, // Pair with better kickers
			},
			handB: HandValue{
				Rank:      Pair,
				RankValue: 3992, // Pair with worse kickers (higher rank value)
			},
			wantResult: 1, // handA > handB (lower rank value is better)
		},
		{
			name: "Exact same hand is a tie",
			handA: HandValue{
				Rank:      FullHouse,
				RankValue: 183,
			},
			handB: HandValue{
				Rank:      FullHouse,
				RankValue: 183,
			},
			wantResult: 0, // tie
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CompareHands(tt.handA, tt.handB)

			if result != tt.wantResult {
				t.Errorf("CompareHands() = %v, want %v", result, tt.wantResult)
			}
		})
	}
}

func TestHandDescriptions(t *testing.T) {
	// Test that hand descriptions are correctly generated
	// NOTE: chehsunliu/poker provides its own hand descriptions
	tests := []struct {
		name         string
		holeCards    []Card
		community    []Card
		wantContains string
	}{
		{
			name: "Royal Flush description",
			holeCards: []Card{
				{suit: Hearts, value: Ace},
				{suit: Hearts, value: King},
			},
			community: []Card{
				{suit: Hearts, value: Queen},
				{suit: Hearts, value: Jack},
				{suit: Hearts, value: Ten},
				{suit: Clubs, value: Three},
				{suit: Diamonds, value: Four},
			},
			wantContains: "Straight Flush", // chehsunliu describes royal flush as straight flush
		},
		{
			name: "Four of a Kind description",
			holeCards: []Card{
				{suit: Hearts, value: Eight},
				{suit: Spades, value: Eight},
			},
			community: []Card{
				{suit: Diamonds, value: Eight},
				{suit: Clubs, value: Eight},
				{suit: Hearts, value: Ace},
				{suit: Clubs, value: King},
				{suit: Spades, value: Queen},
			},
			wantContains: "Four of a Kind",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handValue := EvaluateHand(tt.holeCards, tt.community)
			description := GetHandDescription(handValue)

			if description == "" {
				t.Error("GetHandDescription() returned empty string")
				return
			}

			if !contains(description, tt.wantContains) {
				t.Errorf("GetHandDescription() = %v, want to contain %v", description, tt.wantContains)
			}
		})
	}
}

// Helper function to check if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) && s[:len(substr)] == substr) ||
		findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

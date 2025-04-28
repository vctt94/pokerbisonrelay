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
			wantRank:  RoyalFlush,
			wantValue: 14, // Ace high
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
			wantValue: 9, // Nine high
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
			wantValue: 14, // Four Aces
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
			wantValue: 13, // Kings full of Nines
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
			wantValue: 14, // Ace-high flush
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
			wantValue: 9, // Nine-high straight
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
			wantValue: 12, // Three Queens
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
			wantValue: 14, // Aces and Kings
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
			wantValue: 11, // Pair of Jacks
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
			wantValue: 14, // Ace high
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
	tests := []struct {
		name       string
		handA      HandValue
		handB      HandValue
		wantResult int
	}{
		{
			name: "Royal Flush beats Straight Flush",
			handA: HandValue{
				Rank:      RoyalFlush,
				RankValue: 14,
			},
			handB: HandValue{
				Rank:      StraightFlush,
				RankValue: 13,
			},
			wantResult: 1, // handA > handB
		},
		{
			name: "Four of a Kind beats Full House",
			handA: HandValue{
				Rank:      FourOfAKind,
				RankValue: 10,
			},
			handB: HandValue{
				Rank:      FullHouse,
				RankValue: 14,
			},
			wantResult: 1, // handA > handB
		},
		{
			name: "Higher Four of a Kind beats lower Four of a Kind",
			handA: HandValue{
				Rank:      FourOfAKind,
				RankValue: 10,
			},
			handB: HandValue{
				Rank:      FourOfAKind,
				RankValue: 9,
			},
			wantResult: 1, // handA > handB
		},
		{
			name: "Same rank with higher kicker wins",
			handA: HandValue{
				Rank:      Pair,
				RankValue: 14,
				Kickers:   []int{13, 12, 10},
			},
			handB: HandValue{
				Rank:      Pair,
				RankValue: 14,
				Kickers:   []int{13, 12, 9},
			},
			wantResult: 1, // handA > handB
		},
		{
			name: "Exact same hand is a tie",
			handA: HandValue{
				Rank:      FullHouse,
				RankValue: 10,
				Kickers:   []int{9},
			},
			handB: HandValue{
				Rank:      FullHouse,
				RankValue: 10,
				Kickers:   []int{9},
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
	tests := []struct {
		name         string
		handValue    HandValue
		wantContains string
	}{
		{
			name: "Royal Flush description",
			handValue: HandValue{
				Rank:      RoyalFlush,
				RankValue: 14,
				BestHand: []Card{
					{suit: Hearts, value: Ace},
					{suit: Hearts, value: King},
					{suit: Hearts, value: Queen},
					{suit: Hearts, value: Jack},
					{suit: Hearts, value: Ten},
				},
			},
			wantContains: "Royal Flush",
		},
		{
			name: "Four of a Kind description",
			handValue: HandValue{
				Rank:      FourOfAKind,
				RankValue: 8,
				BestHand: []Card{
					{suit: Hearts, value: Eight},
					{suit: Spades, value: Eight},
					{suit: Diamonds, value: Eight},
					{suit: Clubs, value: Eight},
					{suit: Hearts, value: Ace},
				},
			},
			wantContains: "Four of a Kind",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			description := GetHandDescription(tt.handValue)

			if description == "" {
				t.Error("GetHandDescription() returned empty string")
			}

			if description != tt.wantContains && description[:len(tt.wantContains)] != tt.wantContains {
				t.Errorf("GetHandDescription() = %v, want to contain %v", description, tt.wantContains)
			}
		})
	}
}

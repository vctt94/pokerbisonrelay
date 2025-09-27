package server

import (
	"context"
	"fmt"

	"github.com/vctt94/pokerbisonrelay/pkg/poker"
	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func (s *Server) StartGameStream(req *pokerrpc.StartGameStreamRequest, stream pokerrpc.PokerService_StartGameStreamServer) error {
	// Register the stream
	s.gameStreamsMu.Lock()
	if s.gameStreams[req.TableId] == nil {
		s.gameStreams[req.TableId] = make(map[string]pokerrpc.PokerService_StartGameStreamServer)
	}
	s.gameStreams[req.TableId][req.PlayerId] = stream
	s.gameStreamsMu.Unlock()

	// Remove stream when done
	defer func() {
		s.gameStreamsMu.Lock()
		if tableStreams, exists := s.gameStreams[req.TableId]; exists {
			delete(tableStreams, req.PlayerId)
			if len(tableStreams) == 0 {
				delete(s.gameStreams, req.TableId)
			}
		}
		s.gameStreamsMu.Unlock()
	}()

	// Send initial game state
	gameState, err := s.buildGameState(req.TableId, req.PlayerId)
	if err != nil {
		return err
	}

	if err := stream.Send(gameState); err != nil {
		return err
	}

	// Keep stream open and wait for context cancellation
	// Game state updates will be sent via the notification system when events occur
	ctx := stream.Context()
	<-ctx.Done()
	return nil
}

func (s *Server) MakeBet(ctx context.Context, req *pokerrpc.MakeBetRequest) (*pokerrpc.MakeBetResponse, error) {
	s.mu.RLock()
	table, ok := s.tables[req.TableId]
	s.mu.RUnlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	if !table.IsGameStarted() {
		return nil, status.Error(codes.FailedPrecondition, "game not started")
	}

	if table.GetCurrentPlayerID() != req.PlayerId {
		return nil, status.Error(codes.FailedPrecondition, "not your turn")
	}

	if err := table.MakeBet(req.PlayerId, req.Amount); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	balance, err := s.db.GetPlayerBalance(req.PlayerId)
	if err != nil {
		return nil, err
	}

	if evt, err := CollectGameEventSnapshot(GameEventTypeBetMade, s, req.TableId, req.PlayerId, req.Amount, map[string]interface{}{
		"message": fmt.Sprintf("Player %s bet %d chips", req.PlayerId, req.Amount),
	}); err == nil {
		s.eventProcessor.PublishEvent(evt)
	} else {
		s.log.Errorf("Failed to collect event snapshot: %v", err)
	}

	return &pokerrpc.MakeBetResponse{
		Success:    true,
		Message:    "Bet placed successfully",
		NewBalance: balance,
	}, nil
}

func (s *Server) FoldBet(ctx context.Context, req *pokerrpc.FoldBetRequest) (*pokerrpc.FoldBetResponse, error) {
	s.mu.RLock()
	table, ok := s.tables[req.TableId]
	s.mu.RUnlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}
	if !table.IsGameStarted() {
		return nil, status.Error(codes.FailedPrecondition, "game not started")
	}
	if table.GetCurrentPlayerID() != req.PlayerId {
		return nil, status.Error(codes.FailedPrecondition, "not your turn")
	}
	if err := table.HandleFold(req.PlayerId); err != nil {
		return nil, status.Error(codes.Internal, "failed to process fold: "+err.Error())
	}

	if evt, err := CollectGameEventSnapshot(GameEventTypePlayerFolded, s, req.TableId, req.PlayerId, 0, map[string]interface{}{
		"message": fmt.Sprintf("Player %s folded", req.PlayerId),
	}); err == nil {
		s.eventProcessor.PublishEvent(evt)
	} else {
		s.log.Errorf("Failed to collect event snapshot: %v", err)
	}

	return &pokerrpc.FoldBetResponse{Success: true, Message: "Folded successfully"}, nil
}

// Call implements the Call RPC method
func (s *Server) CallBet(ctx context.Context, req *pokerrpc.CallBetRequest) (*pokerrpc.CallBetResponse, error) {
	s.mu.RLock()
	table, ok := s.tables[req.TableId]
	s.mu.RUnlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}
	if !table.IsGameStarted() {
		return nil, status.Error(codes.FailedPrecondition, "game not started")
	}
	if table.GetCurrentPlayerID() != req.PlayerId {
		return nil, status.Error(codes.FailedPrecondition, "not your turn")
	}

	// Determine how many chips the player actually needs to add (delta) to call.
	var prevBet int64 = 0
	if game := table.GetGame(); game != nil {
		for _, p := range game.GetPlayers() {
			if p.ID == req.PlayerId {
				prevBet = p.HasBet
				break
			}
		}
	}
	// Capture current bet before performing the call so we know the target amount.
	currentBet := table.GetCurrentBet()

	if err := table.HandleCall(req.PlayerId); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	// Calculate delta based on player's previous bet.
	delta := currentBet - prevBet
	if delta < 0 {
		delta = 0 // safety — shouldn't happen
	}

	if evt, err := CollectGameEventSnapshot(GameEventTypeCallMade, s, req.TableId, req.PlayerId, delta, map[string]interface{}{
		"message": fmt.Sprintf("Player %s called %d chips", req.PlayerId, delta),
	}); err == nil {
		s.eventProcessor.PublishEvent(evt)
	} else {
		s.log.Errorf("Failed to collect event snapshot: %v", err)
	}

	return &pokerrpc.CallBetResponse{Success: true, Message: "Call successful"}, nil
}

// Check implements the Check RPC method
func (s *Server) CheckBet(ctx context.Context, req *pokerrpc.CheckBetRequest) (*pokerrpc.CheckBetResponse, error) {
	s.mu.RLock()
	table, ok := s.tables[req.TableId]
	s.mu.RUnlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}
	if !table.IsGameStarted() {
		return nil, status.Error(codes.FailedPrecondition, "game not started")
	}
	if table.GetCurrentPlayerID() != req.PlayerId {
		return nil, status.Error(codes.FailedPrecondition, "not your turn")
	}

	if err := table.HandleCheck(req.PlayerId); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	if evt, err := CollectGameEventSnapshot(GameEventTypeCheckMade, s, req.TableId, req.PlayerId, 0, map[string]interface{}{
		"message": fmt.Sprintf("Player %s checked", req.PlayerId),
	}); err == nil {
		s.eventProcessor.PublishEvent(evt)
	} else {
		s.log.Errorf("Failed to collect event snapshot: %v", err)
	}

	return &pokerrpc.CheckBetResponse{Success: true, Message: "Check successful"}, nil
}

// buildPlayerForUpdate creates a Player proto message with appropriate card visibility
func (s *Server) buildPlayerForUpdate(p *poker.Player, requestingPlayerID string, game *poker.Game) *pokerrpc.Player {
	player := &pokerrpc.Player{
		Id:         p.ID,
		Balance:    p.Balance,
		IsReady:    p.IsReady,
		Folded:     p.HasFolded,
		CurrentBet: p.HasBet,
	}

	// Early return if game doesn't exist or player has no cards
	if game == nil {
		return player
	}

	// Show cards if it's the requesting player's own data (except during NEW_HAND_DEALING to avoid race conditions)
	// OR during showdown for all players
	if p.ID == requestingPlayerID {
		// Show own cards during all active game phases (not just showdown)
		if game.GetPhase() != pokerrpc.GamePhase_NEW_HAND_DEALING && len(p.Hand) > 0 {
			player.Hand = make([]*pokerrpc.Card, len(p.Hand))
			for i, card := range p.Hand {
				player.Hand[i] = &pokerrpc.Card{
					Suit:  card.GetSuit(),
					Value: card.GetValue(),
				}
			}
			s.log.Debugf("DEBUG: Showing %d cards for player %s (own cards, phase=%v)", len(p.Hand), p.ID, game.GetPhase())
		} else {
			// Debug: Log why cards are not being shown
			s.log.Debugf("DEBUG: Not showing cards for player %s: phase=%v, handSize=%d", p.ID, game.GetPhase(), len(p.Hand))
		}
	} else if game.GetPhase() == pokerrpc.GamePhase_SHOWDOWN && len(p.Hand) > 0 {
		// Show other players' cards only during showdown
		player.Hand = make([]*pokerrpc.Card, len(p.Hand))
		for i, card := range p.Hand {
			player.Hand[i] = &pokerrpc.Card{
				Suit:  card.GetSuit(),
				Value: card.GetValue(),
			}
		}
	}

	// Include hand description during showdown
	if game != nil && game.GetPhase() == pokerrpc.GamePhase_SHOWDOWN && p.HandDescription != "" {
		player.HandDescription = p.HandDescription
	}

	return player
}

// buildPlayers creates a slice of Player proto messages with appropriate card visibility
func (s *Server) buildPlayers(tablePlayers []*poker.Player, game *poker.Game, requestingPlayerID string) []*pokerrpc.Player {
	players := make([]*pokerrpc.Player, 0, len(tablePlayers))
	for _, p := range tablePlayers {
		player := s.buildPlayerForUpdate(p, requestingPlayerID, game)
		players = append(players, player)
	}
	return players
}

// buildGameStateForPlayer creates a GameUpdate with all the necessary data for a specific player
func (s *Server) buildGameStateForPlayer(table *poker.Table, game *poker.Game, requestingPlayerID string) *pokerrpc.GameUpdate {
	// Build players list from users and game players
	var players []*pokerrpc.Player
	if game != nil {
		players = s.buildPlayers(game.GetPlayers(), game, requestingPlayerID)
	} else {
		// If no game, build from table users
		users := table.GetUsers()
		players = make([]*pokerrpc.Player, 0, len(users))
		for _, user := range users {
			players = append(players, &pokerrpc.Player{
				Id:      user.ID,
				Balance: 0, // No poker chips when no game - Balance field should be poker chips, not DCR
				IsReady: user.IsReady,

				Hand: make([]*pokerrpc.Card, 0), // Empty hand when no game
			})
		}
	}

	// Build community cards slice
	communityCards := make([]*pokerrpc.Card, 0)
	var pot int64 = 0
	if game != nil {
		pot = game.GetPot()
		for _, c := range game.GetCommunityCards() {
			communityCards = append(communityCards, &pokerrpc.Card{
				Suit:  c.GetSuit(),
				Value: c.GetValue(),
			})
		}
	}

	var currentPlayerID string
	if table.IsGameStarted() && game != nil {
		// Only expose current player when action is valid (not during setup or showdown)
		phase := game.GetPhase()
		if phase != pokerrpc.GamePhase_NEW_HAND_DEALING && phase != pokerrpc.GamePhase_SHOWDOWN {
			currentPlayerID = table.GetCurrentPlayerID()
		}
	}

	return &pokerrpc.GameUpdate{
		TableId:         table.GetConfig().ID,
		Phase:           table.GetGamePhase(),
		PhaseName:       table.GetGamePhase().String(),
		Players:         players,
		CommunityCards:  communityCards,
		Pot:             pot,
		CurrentBet:      table.GetCurrentBet(),
		CurrentPlayer:   currentPlayerID,
		GameStarted:     table.IsGameStarted(),
		PlayersRequired: int32(table.GetMinPlayers()),
		PlayersJoined:   int32(len(table.GetUsers())),
	}
}

func (s *Server) GetGameState(ctx context.Context, req *pokerrpc.GetGameStateRequest) (*pokerrpc.GetGameStateResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	table, ok := s.tables[req.TableId]
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	// Process any player timeouts before building the state.
	table.HandleTimeouts()

	// Extract requesting player ID from context metadata
	requestingPlayerID := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if playerIDs := md.Get("player-id"); len(playerIDs) > 0 {
			requestingPlayerID = playerIDs[0]
		}
	}

	game := table.GetGame()

	return &pokerrpc.GetGameStateResponse{
		GameState: s.buildGameStateForPlayer(table, game, requestingPlayerID),
	}, nil
}

// convertGRPCCardToInternal converts a gRPC Card to internal Card format
func convertGRPCCardToInternal(grpcCard *pokerrpc.Card) (poker.Card, error) {
	if grpcCard == nil {
		return poker.Card{}, fmt.Errorf("card is nil")
	}

	// Convert suit string to internal Suit type
	var suit poker.Suit
	switch grpcCard.Suit {
	case "♠", "s", "S", "spades", "Spades":
		suit = poker.Spades
	case "♥", "h", "H", "hearts", "Hearts":
		suit = poker.Hearts
	case "♦", "d", "D", "diamonds", "Diamonds":
		suit = poker.Diamonds
	case "♣", "c", "C", "clubs", "Clubs":
		suit = poker.Clubs
	default:
		return poker.Card{}, fmt.Errorf("invalid suit: %s", grpcCard.Suit)
	}

	// Convert value string to internal Value type
	var value poker.Value
	switch grpcCard.Value {
	case "A", "a", "ace", "Ace":
		value = poker.Ace
	case "K", "k", "king", "King":
		value = poker.King
	case "Q", "q", "queen", "Queen":
		value = poker.Queen
	case "J", "j", "jack", "Jack":
		value = poker.Jack
	case "10", "T", "t", "ten", "Ten":
		value = poker.Ten
	case "9", "nine", "Nine":
		value = poker.Nine
	case "8", "eight", "Eight":
		value = poker.Eight
	case "7", "seven", "Seven":
		value = poker.Seven
	case "6", "six", "Six":
		value = poker.Six
	case "5", "five", "Five":
		value = poker.Five
	case "4", "four", "Four":
		value = poker.Four
	case "3", "three", "Three":
		value = poker.Three
	case "2", "two", "Two":
		value = poker.Two
	default:
		return poker.Card{}, fmt.Errorf("invalid value: %s", grpcCard.Value)
	}

	// Create the card using a helper function since fields are unexported
	return poker.NewCardFromSuitValue(suit, value), nil
}

func (s *Server) EvaluateHand(ctx context.Context, req *pokerrpc.EvaluateHandRequest) (*pokerrpc.EvaluateHandResponse, error) {
	// Convert gRPC cards to internal Card format
	cards := make([]poker.Card, len(req.Cards))
	for i, grpcCard := range req.Cards {
		card, err := convertGRPCCardToInternal(grpcCard)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid card at index %d: %v", i, err))
		}
		cards[i] = card
	}

	// We need at least 5 cards to evaluate a hand
	if len(cards) < 5 {
		return nil, status.Error(codes.InvalidArgument, "need at least 5 cards to evaluate a hand")
	}

	// For hand evaluation, we'll treat the first 2 cards as hole cards
	// and the rest as community cards (this is a simplification)
	var holeCards, communityCards []poker.Card
	if len(cards) == 5 {
		// If exactly 5 cards, evaluate them all as community cards with empty hole cards
		holeCards = []poker.Card{}
		communityCards = cards
	} else if len(cards) >= 7 {
		// Standard Texas Hold'em: 2 hole + 5 community
		holeCards = cards[:2]
		communityCards = cards[2:7]
	} else {
		// 6 cards: 2 hole + 4 community (incomplete hand)
		holeCards = cards[:2]
		communityCards = cards[2:]
	}

	// Evaluate the hand
	handValue := poker.EvaluateHand(holeCards, communityCards)

	// Convert best hand back to gRPC format
	bestHandGRPC := make([]*pokerrpc.Card, len(handValue.BestHand))
	for i, card := range handValue.BestHand {
		bestHandGRPC[i] = &pokerrpc.Card{
			Suit:  card.GetSuit(),
			Value: card.GetValue(),
		}
	}

	return &pokerrpc.EvaluateHandResponse{
		Rank:        handValue.HandRank,
		Description: handValue.HandDescription,
		BestHand:    bestHandGRPC,
	}, nil
}

func (s *Server) GetLastWinners(ctx context.Context, req *pokerrpc.GetLastWinnersRequest) (*pokerrpc.GetLastWinnersResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	table, ok := s.tables[req.TableId]
	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}
	last := table.GetLastShowdown()
	if last == nil {
		return &pokerrpc.GetLastWinnersResponse{
			Winners: []*pokerrpc.Winner{},
		}, nil
	}

	winners := make([]*pokerrpc.Winner, 0, len(last.WinnerInfo))

	// If game hasn't started or is nil, fall back to last showdown if available.
	if !table.IsGameStarted() || table.GetGame() == nil {
		s.log.Debugf("GetLastWinners: table %s returning cached showdown: winners=%d pot=%d", req.TableId, len(last.WinnerInfo), last.TotalPot)
		for _, wi := range last.WinnerInfo {
			winners = append(winners, &pokerrpc.Winner{PlayerId: wi.PlayerId, Winnings: wi.Winnings, HandRank: wi.HandRank, BestHand: wi.BestHand})
		}
		return &pokerrpc.GetLastWinnersResponse{Winners: winners}, nil
	}

	game := table.GetGame()

	s.log.Debugf("GetLastWinners: table %s game phase=%v", req.TableId, game.GetPhase())
	for _, wi := range last.WinnerInfo {
		winners = append(winners, &pokerrpc.Winner{PlayerId: wi.PlayerId, Winnings: wi.Winnings, HandRank: wi.HandRank, BestHand: wi.BestHand})
	}
	return &pokerrpc.GetLastWinnersResponse{Winners: winners}, nil

}

func (s *Server) ShowCards(ctx context.Context, req *pokerrpc.ShowCardsRequest) (*pokerrpc.ShowCardsResponse, error) {
	s.mu.RLock()
	table, ok := s.tables[req.TableId]
	s.mu.RUnlock()

	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	// Verify player is at the table
	user := table.GetUser(req.PlayerId)
	if user == nil {
		return nil, status.Error(codes.FailedPrecondition, "player not at table")
	}

	// Broadcast card visibility notification to all players at the table
	s.broadcastNotificationToTable(req.TableId, &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_CARDS_SHOWN,
		PlayerId: req.PlayerId,
		TableId:  req.TableId,
		Message:  fmt.Sprintf("%s is showing their cards", req.PlayerId),
	})

	return &pokerrpc.ShowCardsResponse{
		Success: true,
		Message: "Cards shown to other players",
	}, nil
}

func (s *Server) HideCards(ctx context.Context, req *pokerrpc.HideCardsRequest) (*pokerrpc.HideCardsResponse, error) {
	s.mu.RLock()
	table, ok := s.tables[req.TableId]
	s.mu.RUnlock()

	if !ok {
		return nil, status.Error(codes.NotFound, "table not found")
	}

	// Verify player is at the table
	user := table.GetUser(req.PlayerId)
	if user == nil {
		return nil, status.Error(codes.FailedPrecondition, "player not at table")
	}

	// Broadcast card visibility notification to all players at the table
	s.broadcastNotificationToTable(req.TableId, &pokerrpc.Notification{
		Type:     pokerrpc.NotificationType_CARDS_HIDDEN,
		PlayerId: req.PlayerId,
		TableId:  req.TableId,
		Message:  fmt.Sprintf("%s is hiding their cards", req.PlayerId),
	})

	return &pokerrpc.HideCardsResponse{
		Success: true,
		Message: "Cards hidden from other players",
	}, nil
}

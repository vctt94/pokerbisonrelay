package poker

import (
    "testing"
    "time"

    "github.com/stretchr/testify/require"
    "github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
)

// Ensure that when multiple players are all-in pre-flop, the game
// automatically deals remaining community cards and performs showdown
// without panicking.
func TestPreFlopAllInAutoDealShowdown(t *testing.T) {
    cfg := GameConfig{
        NumPlayers:     2,
        StartingChips:  100,
        SmallBlind:     10,
        BigBlind:       20,
        Seed:           1,
        AutoStartDelay: 0,
        TimeBank:       0,
        Log:            createTestLogger(),
    }
    game, err := NewGame(cfg)
    require.NoError(t, err)

    users := []*User{
        NewUser("p1", "p1", 0, 0),
        NewUser("p2", "p2", 0, 1),
    }
    game.SetPlayers(users)

    // Simulate pre-flop all-in by both players with some bets recorded
    game.phase = pokerrpc.GamePhase_PRE_FLOP
    game.communityCards = nil
    game.potManager = NewPotManager(2)

    // Put some chips in to form a pot
    game.potManager.AddBet(0, 50, game.players)
    game.potManager.AddBet(1, 50, game.players)

    // Mark both players as all-in and not folded
    game.players[0].stateMachine.Dispatch(playerStateAllIn)
    game.players[1].stateMachine.Dispatch(playerStateAllIn)
    game.players[0].LastAction = time.Now()
    game.players[1].LastAction = time.Now()

    // Call showdown; should auto-deal to 5 community cards and not error
    res, err := game.handleShowdown()
    require.NoError(t, err)
    require.NotNil(t, res)

    if got := len(game.communityCards); got != 5 {
        t.Fatalf("expected 5 community cards to be dealt, got %d", got)
    }

    // Total pot equals sum of bets (100)
    require.EqualValues(t, int64(100), res.TotalPot)
}

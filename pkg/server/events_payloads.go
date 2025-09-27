package server

import "github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"

// Each event carries exactly one payload implementing this interface.
type EventPayload interface {
	Kind() GameEventType
}

// ---------- Generic/game-wide payloads ----------

type ShowdownPayload struct {
	Winners []*pokerrpc.Winner
	Pot     int64
}

func (ShowdownPayload) Kind() GameEventType { return GameEventTypeShowdownResult }

type GameStartedPayload struct {
	PlayerIDs []string // optional; handlers don't require, but useful
}

func (GameStartedPayload) Kind() GameEventType { return GameEventTypeGameStarted }

type GameEndedPayload struct {
	Reason  string             // optional
	Winners []*pokerrpc.Winner // optional; tournament summary, etc.
}

func (GameEndedPayload) Kind() GameEventType { return GameEventTypeGameEnded }

type NewHandStartedPayload struct {
	HandID    uint64 // optional
	DealerPos int    // optional
}

func (NewHandStartedPayload) Kind() GameEventType { return GameEventTypeNewHandStarted }

// ---------- Action payloads ----------

type BetMadePayload struct {
	PlayerID string
	Amount   int64
}

func (BetMadePayload) Kind() GameEventType { return GameEventTypeBetMade }

type CallMadePayload struct {
	PlayerID string
	Amount   int64 // amount that was called to (or put in)
}

func (CallMadePayload) Kind() GameEventType { return GameEventTypeCallMade }

type CheckMadePayload struct {
	PlayerID string
}

func (CheckMadePayload) Kind() GameEventType { return GameEventTypeCheckMade }

type PlayerFoldedPayload struct {
	PlayerID string
}

func (PlayerFoldedPayload) Kind() GameEventType { return GameEventTypePlayerFolded }

type PlayerReadyPayload struct {
	PlayerID string // see note below; usually string
}

func (PlayerReadyPayload) Kind() GameEventType { return GameEventTypePlayerReady }

// If "ready" is binary+who, prefer this simpler one instead of boolOrString:
type PlayerMarkedReadyPayload struct {
	PlayerID string
	Ready    bool
}

func (PlayerMarkedReadyPayload) Kind() GameEventType { return GameEventTypePlayerReady }

type PlayerJoinedPayload struct {
	PlayerID string
}

func (PlayerJoinedPayload) Kind() GameEventType { return GameEventTypePlayerJoined }

type PlayerLeftPayload struct {
	PlayerID string
}

func (PlayerLeftPayload) Kind() GameEventType { return GameEventTypePlayerLeft }

package server

import (
	"testing"

	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
)

// TestEventProcessorStartPublishStop verifies that events can be queued after
// the processor is started and that Stop terminates cleanly.
func TestEventProcessorStartPublishStop(t *testing.T) {
	s := newBareServer()

	// Create an event processor with zero workers so queued items remain for inspection.
	ep := NewEventProcessor(s, 2, 0)

	// Publish before start should be dropped and not panic.
	ep.PublishEvent(&GameEvent{Type: pokerrpc.NotificationType_BET_MADE, TableID: "tid"})

	ep.Start()

	// Publish after start – with no workers the queue should buffer the event.
	evt := &GameEvent{Type: pokerrpc.NotificationType_PLAYER_READY, TableID: "tid"}
	ep.PublishEvent(evt)

	if len(ep.queue) != 1 {
		t.Fatalf("expected 1 event in queue, got %d", len(ep.queue))
	}

	// Stop must not panic and should flip the started flag allowing Start to be idempotent.
	ep.Stop()
	ep.Stop() // call twice to ensure idempotency
}

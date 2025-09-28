package server

import "testing"

// TestEventProcessorStartPublishStop verifies that events can be queued after
// the processor is started and that Stop terminates cleanly.
func TestEventProcessorStartPublishStop(t *testing.T) {
	s := newBareServer()

	// Create an event processor with zero workers so queued items remain for inspection.
	ep := NewEventProcessor(s, 2, 0)

	// Publish before start should be dropped and not panic.
	ep.PublishEvent(&GameEvent{Type: GameEventTypeBetMade, TableID: "tid"})

	ep.Start()

	// Publish after start – with no workers the queue should buffer the event.
	evt := &GameEvent{Type: GameEventTypePlayerReady, TableID: "tid"}
	ep.PublishEvent(evt)

	if len(ep.queue) != 1 {
		t.Fatalf("expected 1 event in queue, got %d", len(ep.queue))
	}

	// Stop must not panic and should flip the started flag allowing Start to be idempotent.
	ep.Stop()
	ep.Stop() // call twice to ensure idempotency
}

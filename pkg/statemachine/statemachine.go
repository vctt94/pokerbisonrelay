package statemachine

import (
	"sync"
)

// StateEvent represents different state machine events for callbacks
type StateEvent int

const (
	StateEntered StateEvent = iota
	StateExited
	TransitionRequested
)

// StateFn represents a state function following Rob Pike's pattern
// Now includes an optional callback parameter that can be nil
type StateFn[T any] func(*T, func(stateName string, event StateEvent)) StateFn[T]

// StateMachine is a simple, thread-safe state machine wrapper following Rob Pike's pattern
// State functions are the states themselves, and each returns the next state function
type StateMachine[T any] struct {
	// Core state machine fields
	entity  *T           // Reference to the entity
	stateFn StateFn[T]   // Current state function
	mutex   sync.RWMutex // Thread safety
}

// NewStateMachine creates a new state machine for the given entity
func NewStateMachine[T any](entity *T, initialStateFn StateFn[T]) *StateMachine[T] {
	return &StateMachine[T]{
		entity:  entity,
		stateFn: initialStateFn,
	}
}

// Dispatch calls the current state function once and transitions to the returned state
// callback is optional and can be nil
func (sm *StateMachine[T]) Dispatch(callback func(stateName string, event StateEvent)) {
	sm.mutex.Lock()
	currentStateFn := sm.stateFn
	sm.mutex.Unlock()

	if currentStateFn == nil {
		return
	}

	// Execute the state function to get the next state
	nextStateFn := currentStateFn(sm.entity, callback)

	// Update to the next state
	sm.mutex.Lock()
	sm.stateFn = nextStateFn
	sm.mutex.Unlock()
}

// GetCurrentState returns the current state function (thread-safe)
func (sm *StateMachine[T]) GetCurrentState() StateFn[T] {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	return sm.stateFn
}

// SetState sets the state function without triggering callbacks
func (sm *StateMachine[T]) SetState(stateFn StateFn[T]) {
	sm.mutex.Lock()
	sm.stateFn = stateFn
	sm.mutex.Unlock()

	sm.Dispatch(nil)
}

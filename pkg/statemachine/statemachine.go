package statemachine

import (
	"sync"
)

// StateFn represents a state function following Rob Pike's pattern
type StateFn[T any] func(*T) StateFn[T]

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
// stateFn is optional - if provided, it will be set as the current state before execution
func (sm *StateMachine[T]) Dispatch(stateFn StateFn[T]) {
	sm.mutex.Lock()
	currentStateFn := stateFn
	sm.stateFn = currentStateFn
	sm.mutex.Unlock()

	if currentStateFn == nil {
		return
	}

	// Execute the state function to get the next state
	nextStateFn := currentStateFn(sm.entity)

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
}

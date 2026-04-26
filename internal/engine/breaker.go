package engine

import (
	"math"
	"sync"
	"time"
)

// BreakerState is the public view of the circuit-breaker state machine.
// Stringified into the /readyz JSON payload.
type BreakerState string

const (
	BreakerClosed   BreakerState = "closed"
	BreakerOpen     BreakerState = "open"
	BreakerHalfOpen BreakerState = "half_open"
)

// CircuitBreaker tracks consecutive failures of one phase (discovery or
// managed) and short-circuits calls during cooldown. Per spec
// claude/specs/operations_guide.md §5:
//
//	closed → open      after 3 consecutive failures
//	open → half_open   after the cooldown elapses
//	half_open → closed after one success
//	half_open → open   on any failure (resets cooldown)
//
// Cooldown formula: 1m for the first 3 failures, then 2^(failures-3) * 60s
// capped at 1h. Critical: the exponent is capped at 20 to prevent
// math.Pow overflow into +Inf — that bug existed in earlier prototypes.
type CircuitBreaker struct {
	name string

	mu             sync.Mutex
	state          BreakerState
	failures       int
	openedAt       time.Time
	lastTransition time.Time
}

// NewCircuitBreaker returns a fresh breaker in the closed state.
func NewCircuitBreaker(name string) *CircuitBreaker {
	return &CircuitBreaker{
		name:           name,
		state:          BreakerClosed,
		lastTransition: time.Now(),
	}
}

// Allow reports whether a call should proceed. Side effect: when an open
// breaker's cooldown has elapsed, transitions to half_open so the next
// allowed call gets a chance to test recovery.
func (b *CircuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case BreakerClosed:
		return true
	case BreakerHalfOpen:
		return true
	case BreakerOpen:
		if time.Since(b.openedAt) >= b.cooldownLocked() {
			b.transitionLocked(BreakerHalfOpen)
			return true
		}
		return false
	}
	return false
}

// RecordSuccess advances the breaker on a successful call. closed and
// half_open both go to closed; an unexpected RecordSuccess in the open
// state is treated as a half_open success too (defensive — shouldn't
// happen because Allow rejects open calls).
func (b *CircuitBreaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == BreakerClosed {
		b.failures = 0
		return
	}
	// half_open or open → closed.
	b.transitionLocked(BreakerClosed)
	b.failures = 0
}

// RecordFailure advances the breaker on a failed call.
func (b *CircuitBreaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failures++
	switch b.state {
	case BreakerClosed:
		if b.failures >= 3 {
			b.openedAt = time.Now()
			b.transitionLocked(BreakerOpen)
		}
	case BreakerHalfOpen:
		b.openedAt = time.Now()
		b.transitionLocked(BreakerOpen)
	case BreakerOpen:
		// already open; bump the openedAt to reset the cooldown clock.
		b.openedAt = time.Now()
	}
}

// State returns the current state. Cheap, lock-protected snapshot.
func (b *CircuitBreaker) State() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Name returns the breaker's label (used in /readyz output).
func (b *CircuitBreaker) Name() string { return b.name }

// transitionLocked must be called with b.mu held.
func (b *CircuitBreaker) transitionLocked(next BreakerState) {
	b.state = next
	b.lastTransition = time.Now()
}

// cooldownLocked computes the open-state cooldown. Must be called with
// b.mu held. Per spec §5.2 with the exponent cap at 20.
func (b *CircuitBreaker) cooldownLocked() time.Duration {
	if b.failures <= 3 {
		return 1 * time.Minute
	}
	exponent := math.Min(float64(b.failures-3), 20)
	seconds := math.Pow(2, exponent) * 60
	if seconds > 3600 {
		seconds = 3600
	}
	return time.Duration(seconds) * time.Second
}

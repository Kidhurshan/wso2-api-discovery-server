package engine

import (
	"sync"
	"time"
)

// Phase identifiers used by State accessors. Keep in sync with the
// pipeline_state DB columns.
const (
	PhaseDiscovery  = "discovery"
	PhaseManaged    = "managed"
	PhaseComparison = "comparison"
)

// State is the daemon's live snapshot of phase health. It feeds the
// /readyz handler in internal/health (via the State interface declared
// there) and Round 6's circuit breakers report through it.
//
// Per spec claude/specs/operations_guide.md §4.2 the readiness payload
// includes per-phase last-success times and per-breaker statuses.
type State struct {
	mu            sync.RWMutex
	dbReachable   bool
	lastSuccess   map[string]time.Time
	breakerStates map[string]BreakerState
	breakers      map[string]*CircuitBreaker
}

// NewState returns a default-healthy State (DB reachable, no recorded
// success times yet).
func NewState() *State {
	return &State{
		dbReachable:   true,
		lastSuccess:   make(map[string]time.Time),
		breakerStates: make(map[string]BreakerState),
		breakers:      make(map[string]*CircuitBreaker),
	}
}

// RegisterBreaker associates a CircuitBreaker with a phase label so the
// /readyz handler can report its state without holding a separate handle.
func (s *State) RegisterBreaker(b *CircuitBreaker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.breakers[b.Name()] = b
	s.breakerStates[b.Name()] = b.State()
}

// MarkPhaseSuccess records a fresh success for phase. Called from each
// phase's pipeline after a clean cycle.
func (s *State) MarkPhaseSuccess(phase string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSuccess[phase] = time.Now()
}

// LastSuccess returns the most recent success timestamp for phase, or
// the zero time if none recorded.
func (s *State) LastSuccess(phase string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastSuccess[phase]
}

// BreakerStatuses returns a snapshot map of phase→state for the readiness
// payload. The snapshot is computed by polling each registered breaker
// (so the data is always live, not just the last cached value).
func (s *State) BreakerStatuses() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.breakers))
	for name, b := range s.breakers {
		out[name] = string(b.State())
	}
	return out
}

// AnyBreakerOpen reports whether any registered breaker is currently
// in the open state — used by /readyz to flip to 503.
func (s *State) AnyBreakerOpen() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, b := range s.breakers {
		if b.State() == BreakerOpen {
			return true
		}
	}
	return false
}

// SetDBReachable updates the cached DB reachability. Called by the
// engine's poller goroutine.
func (s *State) SetDBReachable(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dbReachable = v
}

// DBReachable returns the cached DB reachability flag.
func (s *State) DBReachable() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dbReachable
}

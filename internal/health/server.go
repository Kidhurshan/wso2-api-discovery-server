// Package health serves the /healthz (liveness) and /readyz (readiness)
// endpoints on a separate port from the BFF. Per
// claude/specs/operations_guide.md §4.
package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// State is the read-only snapshot the readiness probe consults. Per
// claude/specs/operations_guide.md §4.2 it covers DB reachability,
// per-phase last-success timestamps, and per-breaker statuses.
//
// Round 1 used a minimal stub; this is the full surface from Round 6.
type State interface {
	DBReachable() bool
	BreakerStatuses() map[string]string
	AnyBreakerOpen() bool
	LastSuccess(phase string) time.Time
}

// Phase identifiers used by the readiness payload. Strings live here too
// so internal/engine doesn't need to import this package backwards.
const (
	phaseDiscovery  = "discovery"
	phaseManaged    = "managed"
	phaseComparison = "comparison"
)

// staticState is the bootstrap implementation: fixed DB-reachable flag,
// no breakers, no phase tracking. Used by tests and by the engine in
// case the full State isn't ready yet at startup.
type staticState struct {
	dbReachable bool
	mu          sync.RWMutex
}

// NewStaticState returns a State whose DBReachable answer can be flipped
// at runtime via SetDBReachable. Used as a bootstrap when the rich
// engine.State isn't ready yet (or in tests).
func NewStaticState(initial bool) *staticState {
	return &staticState{dbReachable: initial}
}

func (s *staticState) DBReachable() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dbReachable
}

// SetDBReachable updates the cached reachability flag.
func (s *staticState) SetDBReachable(v bool) {
	s.mu.Lock()
	s.dbReachable = v
	s.mu.Unlock()
}

func (s *staticState) BreakerStatuses() map[string]string { return nil }
func (s *staticState) AnyBreakerOpen() bool               { return false }
func (s *staticState) LastSuccess(_ string) time.Time     { return time.Time{} }

// Server runs the health HTTP server. Lifecycle is tied to the context
// passed to Run.
type Server struct {
	addr  string
	state State
	log   *zap.Logger
}

// New returns a Server bound to addr.
func New(addr string, state State, log *zap.Logger) *Server {
	return &Server{addr: addr, state: state, log: log}
}

// Run starts the HTTP server. It returns nil on graceful shutdown via ctx,
// or the underlying error on listen failure.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.liveness)
	mux.HandleFunc("/readyz", s.readiness)

	srv := &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("health server listening", zap.String("addr", s.addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		<-errCh
		return nil
	case err := <-errCh:
		return err
	}
}

// liveness always returns 200 — the process is up.
func (s *Server) liveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
}

// readinessReport mirrors the spec from operations_guide.md §4.2.
type readinessReport struct {
	Status                string            `json:"status"`
	DatabaseReachable     bool              `json:"database_reachable"`
	CircuitBreakers       map[string]string `json:"circuit_breakers,omitempty"`
	LastDiscoverySuccess  time.Time         `json:"last_discovery_success,omitempty"`
	LastManagedSuccess    time.Time         `json:"last_managed_success,omitempty"`
	LastComparisonSuccess time.Time         `json:"last_comparison_success,omitempty"`
}

// readiness returns 200 only when every dependency reported by State
// looks healthy: DB reachable AND no breaker open. Per spec §4.2.
func (s *Server) readiness(w http.ResponseWriter, _ *http.Request) {
	report := readinessReport{
		DatabaseReachable:     s.state.DBReachable(),
		CircuitBreakers:       s.state.BreakerStatuses(),
		LastDiscoverySuccess:  s.state.LastSuccess(phaseDiscovery),
		LastManagedSuccess:    s.state.LastSuccess(phaseManaged),
		LastComparisonSuccess: s.state.LastSuccess(phaseComparison),
	}

	ready := report.DatabaseReachable && !s.state.AnyBreakerOpen()
	if ready {
		report.Status = "ready"
		w.WriteHeader(http.StatusOK)
	} else {
		report.Status = "not_ready"
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

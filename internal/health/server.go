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

// State is the read-only snapshot the readiness probe consults. The engine
// updates these fields as it runs; concurrent reads are guarded by the mutex
// inside the implementation.
//
// Round 1 ships a minimal version (DB reachable). Round 6 expands it to
// include circuit breaker statuses and per-phase last-success timestamps.
type State interface {
	DBReachable() bool
}

// staticState is the bootstrap implementation used in Round 1: always
// reports the DB as reachable. The real implementation in internal/engine
// will replace this in Round 6.
type staticState struct {
	dbReachable bool
	mu          sync.RWMutex
}

// NewStaticState returns a State whose DBReachable answer can be flipped at
// runtime via SetDBReachable. Used as the engine's bootstrap state.
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

// Server runs the health HTTP server. Lifecycle is tied to the context passed
// to Run.
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

// readinessReport mirrors the future-richer struct in
// claude/specs/operations_guide.md §4.2. Round 1 fills only db_reachable.
type readinessReport struct {
	Status            string `json:"status"`
	DatabaseReachable bool   `json:"database_reachable"`
}

// readiness returns 200 only when every dependency reported by State looks
// healthy. Currently this means just the DB. Round 6 expands the criteria.
func (s *Server) readiness(w http.ResponseWriter, _ *http.Request) {
	report := readinessReport{DatabaseReachable: s.state.DBReachable()}
	if report.DatabaseReachable {
		report.Status = "ready"
		w.WriteHeader(http.StatusOK)
	} else {
		report.Status = "not_ready"
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

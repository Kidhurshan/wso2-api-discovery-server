package bff

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/wso2/api-discovery-server/internal/apim"
	"github.com/wso2/api-discovery-server/internal/config"
	"github.com/wso2/api-discovery-server/internal/store"
)

// Server is the daemon's REST surface. Construct via New, call Run inside a
// goroutine; lifecycle ties to the ctx passed in.
type Server struct {
	cfg          *config.Config
	log          *zap.Logger
	repo         *store.BFFRepo
	introspector *apim.Introspector
	tokens       *tokenCache
}

// New wires the dependencies.
func New(cfg *config.Config, log *zap.Logger, repo *store.BFFRepo, introspector *apim.Introspector) *Server {
	return &Server{
		cfg:          cfg,
		log:          log,
		repo:         repo,
		introspector: introspector,
		tokens: newTokenCache(
			time.Duration(cfg.BFF.TokenCache.TTLSeconds)*time.Second,
			cfg.BFF.TokenCache.MaxEntries,
		),
	}
}

// Run starts the HTTPS server. Returns nil on graceful shutdown via ctx,
// or the underlying error on listen failure.
//
// TLS is mandatory in production; the server fails fast if cert/key paths
// are missing or unreadable. For test environments where mTLS rigor isn't
// needed, point at self-signed material — clients that won't verify can
// pass --insecure.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle("/api/v1/governance/discovery/summary",
		s.authMiddleware(http.HandlerFunc(s.handleSummary)))
	mux.Handle("/api/v1/governance/discovery/apis",
		s.authMiddleware(http.HandlerFunc(s.handleList)))
	mux.Handle("/api/v1/governance/discovery/apis/",
		s.authMiddleware(http.HandlerFunc(s.handleDetail)))
	mux.Handle("/api/v1/governance/discovery/untrafficked",
		s.authMiddleware(http.HandlerFunc(s.handleUntrafficked)))

	srv := &http.Server{
		Addr:              s.cfg.BFF.ListenAddr,
		Handler:           mux,
		ReadTimeout:       time.Duration(s.cfg.BFF.ReadTimeoutSeconds) * time.Second,
		WriteTimeout:      time.Duration(s.cfg.BFF.WriteTimeoutSeconds) * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("bff server listening (TLS)",
			zap.String("addr", s.cfg.BFF.ListenAddr),
			zap.String("cert", s.cfg.BFF.TLSCert),
		)
		err := srv.ListenAndServeTLS(s.cfg.BFF.TLSCert, s.cfg.BFF.TLSKey)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
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

// handleSummary serves GET /summary.
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	summary, err := s.repo.GetSummary(r.Context(), s.cfg.Discovery.SkipInternal)
	if err != nil {
		s.log.Error("get summary", zap.Error(err))
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "summary unavailable"})
		return
	}
	respondJSON(w, http.StatusOK, summary)
}

// handleList serves GET /apis with optional filters.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	filter := store.ListFilter{
		Classification: q.Get("classification"),
		Service:        q.Get("service"),
		Internal:       q.Get("internal"),
		Limit:          limit,
		Offset:         offset,
	}
	if filter.Classification != "" && filter.Classification != "shadow" && filter.Classification != "drift" {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"error": "classification must be 'shadow' or 'drift'",
		})
		return
	}
	if filter.Internal != "" && filter.Internal != "true" && filter.Internal != "false" && filter.Internal != "only" {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"error": "internal must be 'true', 'false', or 'only'",
		})
		return
	}

	result, err := s.repo.ListDiscovered(r.Context(), filter)
	if err != nil {
		s.log.Error("list discovered", zap.Error(err))
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "list unavailable"})
		return
	}
	pageLimit := filter.Limit
	if pageLimit <= 0 {
		pageLimit = 25
	}
	if pageLimit > 100 {
		pageLimit = 100
	}
	resp := map[string]any{
		"count": len(result.List),
		"list":  result.List,
		"pagination": map[string]int{
			"offset": filter.Offset,
			"limit":  pageLimit,
			"total":  result.Total,
		},
	}
	respondJSON(w, http.StatusOK, resp)
}

// handleDetail serves GET /apis/{id}. Path-segment parsing is done
// directly because we don't want a router dependency for one variable.
func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	const prefix = "/api/v1/governance/discovery/apis/"
	idStr := r.URL.Path[len(prefix):]
	if idStr == "" {
		// /apis/ with no id is the list endpoint, but the mux already routed
		// non-trailing-slash to handleList. A trailing slash shouldn't
		// reach detail; treat as 404 to be safe.
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "missing id"})
		return
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"error": "id must be a UUID",
		})
		return
	}
	detail, err := s.repo.GetDiscoveredByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			respondJSON(w, http.StatusNotFound, map[string]string{"error": "discovered API not found"})
			return
		}
		s.log.Error("get detail", zap.String("id", idStr), zap.Error(err))
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "detail unavailable"})
		return
	}
	respondJSON(w, http.StatusOK, detail)
}

// handleUntrafficked serves GET /untrafficked.
func (s *Server) handleUntrafficked(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	items, err := s.repo.ListUntrafficked(r.Context())
	if err != nil {
		s.log.Error("list untrafficked", zap.Error(err))
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "untrafficked unavailable"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"count": len(items),
		"list":  items,
	})
}

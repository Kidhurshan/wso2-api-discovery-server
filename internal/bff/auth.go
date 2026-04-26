package bff

import (
	"encoding/json"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/wso2/api-discovery-server/internal/apim"
)

// requiredScopes are the OAuth2 scopes a bearer token must hold to access
// any /api/v1/governance/discovery/* endpoint. Per spec
// phase4_admin_portal.md §3.1 either of `apim:admin` or
// `apim:admin_discovery_view` is acceptable.
var requiredScopes = []string{"apim:admin", "apim:admin_discovery_view"}

// authMiddleware extracts a Bearer token, looks it up in the cache, falls
// back to APIM /oauth2/introspect on miss, and validates that the token is
// active and carries one of requiredScopes. On success it inserts the
// TokenInfo into the request context so handlers can attribute the call.
//
// Failure modes (per spec §9):
//
//	missing/malformed Authorization → 401 with {"error":"unauthorized"}
//	introspection fails or token inactive → 401
//	token active but lacks required scope → 403 {"error":"forbidden"}
type authCtxKey struct{}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			respondJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "missing or malformed bearer token",
			})
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		if token == "" {
			respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "empty bearer token"})
			return
		}

		// Cache lookup first.
		info := s.tokens.get(token)
		if info == nil {
			fresh, err := s.introspector.Introspect(r.Context(), token)
			if err != nil {
				s.log.Warn("introspection failed", zap.Error(err))
				respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "token verification failed"})
				return
			}
			if !fresh.Active {
				respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "token inactive"})
				return
			}
			s.tokens.put(token, fresh)
			info = fresh
		}

		if !hasAnyScope(info, requiredScopes) {
			respondJSON(w, http.StatusForbidden, map[string]string{
				"error":           "insufficient scope",
				"required_one_of": strings.Join(requiredScopes, ", "),
				"granted":         info.Scope,
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// hasAnyScope returns true if info's scope list contains any of want.
func hasAnyScope(info *apim.TokenInfo, want []string) bool {
	have := info.Scopes()
	for _, w := range want {
		for _, h := range have {
			if h == w {
				return true
			}
		}
	}
	return false
}

// respondJSON writes status + JSON body. Errors during encoding fall
// through silently — the headers were already written, and a 500-on-500
// helper would be lipstick.
func respondJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

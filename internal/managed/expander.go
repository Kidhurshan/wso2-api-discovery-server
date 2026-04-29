package managed

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/wso2/api-discovery-server/internal/apim"
	"github.com/wso2/api-discovery-server/internal/discovery"
)

// apimPlaceholderRe matches WSO2's `{paramName}` style placeholders in
// operation targets. Per claude/specs/phase2_managed_sync.md §6 we collapse
// these to a uniform `/{id}` (Pass A) before applying the Phase 1 normalizer
// (Pass B) — this guarantees Phase 1's discovered_path and Phase 2's
// gateway/backend paths use the same vocabulary so Phase 3 can join them by
// equality on (method, path).
var apimPlaceholderRe = regexp.MustCompile(`\{[^}/]+\}`)

// rawPlaceholderRe is identical to apimPlaceholderRe but with a capture
// group for extracting original placeholder names if a caller wants them.
var rawPlaceholderRe = regexp.MustCompile(`\{([^}/]+)\}`)

// ManagedOperation is one expanded (api, verb, target) tuple, ready to be
// upserted into ads_managed_apis.
//
// Per the redesign: no env_kind, no service_identity, no backend host/port.
// Phase 3 matches on (method, gateway_path | backend_path) only.
type ManagedOperation struct {
	APIID              string
	APIName            string
	APIVersion         string
	APIContext         string
	APIProvider        string
	APILifecycleStatus string

	Method       string
	GatewayPath  string // /prod/1.0.0/item/{id}    — what the client sees
	BackendPath  string // /products/v1/item/{id}   — what the backend sees
	BackendURL   string // raw URL string for debug / display
	AuthType     string
	ThrottlingPolicy string

	Warnings []string
}

// Expander turns one APIDetail into a slice of ManagedOperation rows by
// walking api.Operations and applying the two-pass placeholder normalization.
//
// The normalizer here is the SAME *discovery.Normalizer instance Phase 1
// uses, loaded from the same config — single source of truth for the
// normalization rules.
type Expander struct {
	norm *discovery.Normalizer
}

// NewExpander wires the shared normalizer.
func NewExpander(norm *discovery.Normalizer) *Expander {
	return &Expander{norm: norm}
}

// Expand walks api.Operations and produces one ManagedOperation per
// operation. Each path (both gateway and backend forms) is computed from
// the URL components, then run through Pass A (APIM placeholders → "{id}")
// and Pass B (the shared Phase 1 normalizer).
func (e *Expander) Expand(api *apim.APIDetail) []ManagedOperation {
	rawBackend := backendURL(api)
	backendBase := backendBasePath(rawBackend)

	out := make([]ManagedOperation, 0, len(api.Operations))
	for _, op := range api.Operations {
		gateway := composeGatewayPath(api.Context, api.Version, op.Target)
		backend := backendBase + op.Target

		out = append(out, ManagedOperation{
			APIID:              api.ID,
			APIName:            api.Name,
			APIVersion:         api.Version,
			APIContext:         api.Context,
			APIProvider:        api.Provider,
			APILifecycleStatus: api.LifeCycleStatus,
			Method:             strings.ToUpper(op.Verb),
			GatewayPath:        normalizePath(e.norm, gateway),
			BackendPath:        normalizePath(e.norm, backend),
			BackendURL:         rawBackend,
			AuthType:           op.AuthType,
			ThrottlingPolicy:   op.ThrottlingPolicy,
		})
	}
	return out
}

// normalizePath applies the two-pass collapse: APIM placeholders first,
// then the shared Phase 1 normalizer.
func normalizePath(n *discovery.Normalizer, path string) string {
	passA := apimPlaceholderRe.ReplaceAllString(path, "{id}")
	if n == nil {
		return passA
	}
	return n.Normalize(passA)
}

// composeGatewayPath joins APIM's context, version, and operation target.
//
// Modern WSO2 APIM emits context already including the version segment
// (e.g., context="/orders/1.0.0", version="1.0.0"). Naïvely concatenating
// would duplicate the version. This helper handles both conventions.
func composeGatewayPath(context, version, target string) string {
	versionSuffix := "/" + version
	if version != "" && strings.HasSuffix(context, versionSuffix) {
		return context + target
	}
	if version != "" {
		return context + "/" + version + target
	}
	return context + target
}

// backendBasePath returns the path component of the backend URL, with the
// trailing slash trimmed. Returns "" when the URL is empty, malformed, or
// has no path component.
//
// Examples:
//
//	"http://orders:8080"               → ""
//	"http://orders:8080/"              → ""
//	"http://products:8080/products/v1" → "/products/v1"
//	"https://api.acme.com/v2/"         → "/v2"
func backendBasePath(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.TrimRight(u.Path, "/")
}

// backendURL extracts the production endpoint URL from the API detail.
// Returns "" if absent so callers see an empty backend_url column rather
// than failing the upsert.
func backendURL(api *apim.APIDetail) string {
	if api.EndpointConfig.ProductionEndpoints == nil {
		return ""
	}
	return api.EndpointConfig.ProductionEndpoints.URL
}

// rawPlaceholders extracts the placeholder names from an operation target.
// Useful if a future enhancement wants to display "{customerId}" alongside
// the normalized "{id}" in the UI.
func rawPlaceholders(target string) []string {
	matches := rawPlaceholderRe.FindAllStringSubmatch(target, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

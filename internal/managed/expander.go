package managed

import (
	"regexp"
	"strings"

	"github.com/wso2/api-discovery-server/internal/apim"
	"github.com/wso2/api-discovery-server/internal/discovery"
)

// apimPlaceholderRe matches WSO2's `{paramName}` style placeholders in
// operation targets. Per spec phase2_managed_sync.md §6 we collapse these
// to a uniform `/{id}` (Pass A) before applying the Phase 1 normalizer
// (Pass B) — this guarantees Phase 1's discovered_path and Phase 2's
// gateway_path use the same vocabulary so Phase 3 can join them by
// equality on (method, normalized_path).
var apimPlaceholderRe = regexp.MustCompile(`\{[^}/]+\}`)

// rawPlaceholderRe is identical to apimPlaceholderRe but with a capture
// group, used to extract original placeholder names for the row's
// raw_placeholders[] column.
var rawPlaceholderRe = regexp.MustCompile(`\{([^}/]+)\}`)

// ManagedOperation is one expanded (api, verb, target) tuple, ready to be
// upserted into ads_managed_apis.
type ManagedOperation struct {
	APIID              string
	APIName            string
	APIVersion         string
	APIContext         string
	APIProvider        string
	APILifecycleStatus string

	EnvKind         string
	ServiceIdentity string

	Method             string
	GatewayPath        string
	OperationTarget    string
	RawOperationTarget string
	RawPlaceholders    []string
	AuthType           string
	ThrottlingPolicy   string

	BackendURL          string
	BackendResolvedIP   string
	BackendResolvedPort int

	Warnings []string
}

// Expander turns one APIDetail + ResolverResult into a slice of
// ManagedOperation rows by walking api.Operations and applying the
// two-pass placeholder normalization.
//
// The normalizer here is the SAME *discovery.Normalizer instance Phase 1
// uses, loaded from the same config — single source of truth for
// normalization rules.
type Expander struct {
	norm *discovery.Normalizer
}

// NewExpander wires the shared normalizer.
func NewExpander(norm *discovery.Normalizer) *Expander {
	return &Expander{norm: norm}
}

// Expand walks api.Operations and produces one ManagedOperation per
// operation. Each gateway_path is computed by composeGatewayPath, then run
// through Pass A (APIM placeholders → "{id}") and Pass B (the shared Phase
// 1 normalizer).
func (e *Expander) Expand(api *apim.APIDetail, res *ResolverResult) []ManagedOperation {
	out := make([]ManagedOperation, 0, len(api.Operations))
	for _, op := range api.Operations {
		raw := composeGatewayPath(api.Context, api.Version, op.Target)
		// Pass A: collapse APIM placeholders before path-segment regexes
		// have a chance to look at them, otherwise patterns like
		// `/CUST-{id}` would never match.
		passA := apimPlaceholderRe.ReplaceAllString(raw, "{id}")
		// Pass B: full Phase 1 normalizer (numeric ids, UUIDs, SKUs, ...).
		gatewayPath := e.norm.Normalize(passA)

		var rawPlaceholders []string
		for _, m := range rawPlaceholderRe.FindAllStringSubmatch(op.Target, -1) {
			rawPlaceholders = append(rawPlaceholders, m[1])
		}

		out = append(out, ManagedOperation{
			APIID:              api.ID,
			APIName:            api.Name,
			APIVersion:         api.Version,
			APIContext:         api.Context,
			APIProvider:        api.Provider,
			APILifecycleStatus: api.LifeCycleStatus,

			EnvKind:         res.EnvKind,
			ServiceIdentity: res.ServiceIdentity,

			Method:             strings.ToUpper(op.Verb),
			GatewayPath:        gatewayPath,
			OperationTarget:    op.Target,
			RawOperationTarget: op.Target,
			RawPlaceholders:    rawPlaceholders,
			AuthType:           op.AuthType,
			ThrottlingPolicy:   op.ThrottlingPolicy,

			BackendURL:          backendURL(api),
			BackendResolvedIP:   res.BackendIP,
			BackendResolvedPort: res.BackendPort,

			Warnings: res.Warnings,
		})
	}
	return out
}

// composeGatewayPath joins APIM's context, version, and operation target.
//
// The spec (phase2_managed_sync.md §6) writes the formula as
// "context + / + version + target". In practice, modern WSO2 APIM emits
// context already including the version segment (e.g., context="/orders/1.0.0"
// version="1.0.0"). Naïvely concatenating duplicates the version: the spec's
// formula yields "/orders/1.0.0/1.0.0/orders" for the TechMart layout.
//
// This helper handles both conventions:
//   - if context already ends with "/" + version, use context + target
//   - otherwise, use context + "/" + version + target (spec's older form)
//
// Result is functionally identical to the spec's intent, with no version
// duplication.
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

// backendURL extracts the production endpoint URL from the API detail.
// Returns "" if absent so callers see an empty backend_url column rather
// than failing the upsert.
func backendURL(api *apim.APIDetail) string {
	if api.EndpointConfig.ProductionEndpoints == nil {
		return ""
	}
	return api.EndpointConfig.ProductionEndpoints.URL
}

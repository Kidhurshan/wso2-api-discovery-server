// Package discovery owns the Phase 1 pipeline: pull L7 records from DeepFlow,
// normalize paths, classify identities into the spec's service_identity
// format, merge by (service, method, normalized_path), and upsert into
// ads_discovered_apis.
package discovery

import (
	"strings"

	"github.com/wso2/api-discovery-server/internal/config"
)

// Normalizer applies path-normalization regex rules in declared order. Used
// by Phase 1 (this package) and reused by Phase 2's resolver to keep both
// phases producing identical normalized_path values for the same logical URL.
//
// The regex compilation is done once in config.Validate() and stored on each
// rule's Compiled field; this struct just carries them.
type Normalizer struct {
	Version string
	Rules   []config.NormalizationRule
}

// NewFromConfig wraps the validated rules from cfg.Discovery into a
// Normalizer. cfg.Validate() must have been called first; otherwise the
// Compiled field on each rule is nil and Normalize will panic.
func NewFromConfig(cfg *config.DiscoveryConfig) *Normalizer {
	return &Normalizer{
		Version: cfg.NormalizationRulesMeta.Version,
		Rules:   cfg.NormalizationRules,
	}
}

// Normalize transforms a raw URL path into its normalized form.
//
// Steps (per spec phase1_discovery.md §4.1):
//  1. Strip the query string at the first '?'.
//  2. Trim a single trailing '/' (but keep "/" itself).
//  3. Apply each rule's compiled regex in order, replacing matches with the
//     rule's placeholder. First match per segment wins because regex
//     replacements are non-overlapping by default in Go's regexp.
func (n *Normalizer) Normalize(rawPath string) string {
	if i := strings.IndexByte(rawPath, '?'); i >= 0 {
		rawPath = rawPath[:i]
	}
	if len(rawPath) > 1 && rawPath[len(rawPath)-1] == '/' {
		rawPath = rawPath[:len(rawPath)-1]
	}
	for _, rule := range n.Rules {
		if rule.Compiled == nil {
			continue
		}
		rawPath = rule.Compiled.ReplaceAllString(rawPath, rule.Placeholder)
	}
	return rawPath
}

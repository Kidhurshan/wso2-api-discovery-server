// Package discovery owns the Phase 1 pipeline: pull L7 records from DeepFlow,
// normalize paths, classify identities into the spec's service_identity
// format, merge by (service, method, normalized_path), and upsert into
// ads_discovered_apis.
package discovery

import (
	"regexp"
	"strings"

	"github.com/wso2/api-discovery-server/internal/config"
)

// Normalizer collapses dynamic path segments to a uniform "{id}" placeholder.
//
// Algorithm (per claude/specs/phase1_discovery.md, redesigned to be segment-
// based instead of whole-path regex):
//
//  1. Strip the query string at the first '?'.
//  2. Trim a single trailing '/' (but keep "/" itself).
//  3. Split the path by '/' into segments.
//  4. For each non-empty segment:
//     - If any ExcludePatterns regex matches → keep the segment as-is
//       (used to preserve API version segments like "1.0.0", "v1").
//     - Else if any BuiltinPatterns or UserPatterns regex matches →
//       replace the segment with "{id}".
//     - Else keep the segment as-is.
//  5. Rejoin with '/'.
//
// All patterns are anchored with ^...$ in the config because they match
// against a single segment, not the whole path.
//
// The compiled regexes come from config.Validate(), which populates the
// CompiledBuiltin / CompiledUser / CompiledExclude slices on the config.
type Normalizer struct {
	Version string
	builtin []*regexp.Regexp
	user    []*regexp.Regexp
	exclude []*regexp.Regexp
}

// NewFromConfig wires the Normalizer to the validated patterns.
// cfg.Validate() must have been called first; otherwise the Compiled
// fields are nil and Normalize will pass every segment through unchanged.
func NewFromConfig(cfg *config.DiscoveryConfig) *Normalizer {
	return &Normalizer{
		Version: "v2", // bumped to mark the segment-based redesign
		builtin: cfg.Normalization.CompiledBuiltin,
		user:    cfg.Normalization.CompiledUser,
		exclude: cfg.Normalization.CompiledExclude,
	}
}

// Normalize transforms a raw URL path into its normalized form.
func (n *Normalizer) Normalize(rawPath string) string {
	if i := strings.IndexByte(rawPath, '?'); i >= 0 {
		rawPath = rawPath[:i]
	}
	if len(rawPath) > 1 && rawPath[len(rawPath)-1] == '/' {
		rawPath = rawPath[:len(rawPath)-1]
	}
	if rawPath == "" || rawPath == "/" {
		return rawPath
	}

	segments := strings.Split(rawPath, "/")
	for i, seg := range segments {
		if seg == "" {
			// Preserves leading "/" (segments[0] is empty when path
			// starts with /). Mid-path empty segments would only come
			// from doubled slashes like "//foo"; keeping them here is
			// fine — the normalized path stays faithful to the input.
			continue
		}
		segments[i] = n.normalizeSegment(seg)
	}
	return strings.Join(segments, "/")
}

// normalizeSegment is the per-segment decision: exclude wins over positive
// match. Returns "{id}" for dynamic segments, the original segment otherwise.
func (n *Normalizer) normalizeSegment(seg string) string {
	for _, re := range n.exclude {
		if re.MatchString(seg) {
			return seg
		}
	}
	for _, re := range n.builtin {
		if re.MatchString(seg) {
			return "{id}"
		}
	}
	for _, re := range n.user {
		if re.MatchString(seg) {
			return "{id}"
		}
	}
	return seg
}

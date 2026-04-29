// Package config holds the strongly-typed configuration for the ads daemon.
//
// Field shapes mirror the TOML keys in claude/specs/operations_guide.md §1.2.
// Loading and validation live in load.go and validate.go.
package config

import (
	"regexp"
	"time"
)

// Config is the root configuration tree.
type Config struct {
	ADS        ADSConfig        `toml:"ads"`
	Database   DatabaseConfig   `toml:"database"`
	DeepFlow   DeepFlowConfig   `toml:"deepflow"`
	APIM       APIMConfig       `toml:"apim"`
	Discovery  DiscoveryConfig  `toml:"discovery"`
	Managed    ManagedConfig    `toml:"managed"`
	Comparison ComparisonConfig `toml:"comparison"`
	BFF        BFFConfig        `toml:"bff"`
	Health     HealthConfig     `toml:"health"`
	K8s        K8sConfig        `toml:"k8s"`
	Retention  RetentionConfig  `toml:"retention"`
}

// ADSConfig is the top-level [ads] block.
type ADSConfig struct {
	Name     string `toml:"name"`
	Version  string `toml:"version"`
	LogLevel string `toml:"log_level"` // debug | info | warn | error
}

// DatabaseConfig is the [database] block.
type DatabaseConfig struct {
	Host                  string `toml:"host"`
	Port                  int    `toml:"port"`
	Name                  string `toml:"name"`
	User                  string `toml:"user"`
	Password              string `toml:"password"`
	SSLMode               string `toml:"sslmode"`
	MaxOpenConns          int    `toml:"max_open_conns"`
	MaxIdleConns          int    `toml:"max_idle_conns"`
	ConnectTimeoutSeconds int    `toml:"connect_timeout_seconds"`
}

// DeepFlowConfig is the [deepflow] block.
type DeepFlowConfig struct {
	Enabled            bool   `toml:"enabled"`
	ClickHouseURL      string `toml:"clickhouse_url"`
	ClickHouseUser     string `toml:"clickhouse_user"`
	ClickHousePassword string `toml:"clickhouse_password"`
	VerifySSL          bool   `toml:"verify_ssl"`
	TimeoutSeconds     int    `toml:"timeout_seconds"`
}

// APIMConfig is the [apim] block.
type APIMConfig struct {
	PublisherURL           string `toml:"publisher_url"`
	ServiceAccountUsername string `toml:"service_account_username"`
	ServiceAccountPassword string `toml:"service_account_password"`
	VerifySSL              bool   `toml:"verify_ssl"`
	TimeoutSeconds         int    `toml:"timeout_seconds"`
	IntrospectURL          string `toml:"introspect_url"`
	IntrospectBasicAuth    string `toml:"introspect_basic_auth"`
}

// DiscoveryConfig is the [discovery] block (Phase 1).
type DiscoveryConfig struct {
	PollIntervalMinutes    int                 `toml:"poll_interval_minutes"`
	WindowMinutes          int                 `toml:"window_minutes"`
	StatusMin              int                 `toml:"status_min"`
	StatusMax              int                 `toml:"status_max"`
	SkipInternal           bool                `toml:"skip_internal"`
	MinObservations        int                 `toml:"min_observations"`
	MaxSignaturesPerWindow int                 `toml:"max_signatures_per_window"`
	NoiseFilter            NoiseFilterConfig   `toml:"noise_filter"`
	Normalization          NormalizationConfig `toml:"normalization"`
}

// NoiseFilterConfig is [discovery.noise_filter].
//
// PathPatterns and PathExact are checked Go-side. PathPatterns is a
// substring (CONTAINS) check — drops any path containing the entry as
// a substring (e.g. "/health" drops "/orders/1.0.0/health" too).
// PathExact is an equality check — drops only paths exactly equal to
// the entry (used for paths like "/" where CONTAINS would over-match).
//
// ExcludedPorts and ExcludedDomains are passed to the DeepFlow query
// (server_port NOT IN, request_domain NOT IN).
type NoiseFilterConfig struct {
	PathPatterns    []string `toml:"path_patterns"`
	PathExact       []string `toml:"path_exact"`
	ExcludedPorts   []int    `toml:"excluded_ports"`
	ExcludedDomains []string `toml:"excluded_domains"`
}

// NormalizationConfig is [discovery.normalization].
//
// Algorithm (per-segment):
//  1. Split path by '/'
//  2. For each non-empty segment:
//     - if any ExcludePatterns regex matches → keep the segment as-is
//       (used to preserve API version segments like "1.0.0", "v1")
//     - else if any BuiltinPatterns or UserPatterns regex matches →
//       replace the segment with "{id}"
//     - else keep the segment as-is
//  3. Rejoin with '/'
//
// All patterns are anchored with ^...$ because they match a single
// segment, not the whole path.
//
// VersionPattern is documentation only — the same regex must also be
// the first entry of ExcludePatterns. Validate() warns if they diverge.
type NormalizationConfig struct {
	VersionPattern  string   `toml:"version_pattern"`
	BuiltinPatterns []string `toml:"builtin_patterns"`
	UserPatterns    []string `toml:"user_patterns"`
	ExcludePatterns []string `toml:"exclude_patterns"`

	// Compiled* fields are populated by Validate(). The normalizer reuses
	// the pre-compiled regexes to avoid recompiling on every cycle.
	CompiledBuiltin []*regexp.Regexp `toml:"-"`
	CompiledUser    []*regexp.Regexp `toml:"-"`
	CompiledExclude []*regexp.Regexp `toml:"-"`
}

// ManagedConfig is the [managed] block (Phase 2).
type ManagedConfig struct {
	PollIntervalMinutes int `toml:"poll_interval_minutes"`
	FetchConcurrency    int `toml:"fetch_concurrency"`
}

// PollInterval returns the managed poll interval as a Duration.
func (m ManagedConfig) PollInterval() time.Duration {
	return time.Duration(m.PollIntervalMinutes) * time.Minute
}

// ComparisonConfig is the [comparison] block (Phase 3).
type ComparisonConfig struct {
	FreshnessThresholdMultiplier int `toml:"freshness_threshold_multiplier"`
}

// BFFConfig is the [bff] block.
type BFFConfig struct {
	ListenAddr          string           `toml:"listen_addr"`
	TLSCert             string           `toml:"tls_cert"`
	TLSKey              string           `toml:"tls_key"`
	VerifyClientCert    bool             `toml:"verify_client_cert"`
	ReadTimeoutSeconds  int              `toml:"read_timeout_seconds"`
	WriteTimeoutSeconds int              `toml:"write_timeout_seconds"`
	TokenCache          TokenCacheConfig `toml:"token_cache"`
}

// TokenCacheConfig is [bff.token_cache].
type TokenCacheConfig struct {
	TTLSeconds int `toml:"ttl_seconds"`
	MaxEntries int `toml:"max_entries"`
}

// HealthConfig is the [health] block.
type HealthConfig struct {
	ListenAddr string `toml:"listen_addr"`
}

// K8sConfig is the [k8s] block. When Enabled is false, leader election is
// bypassed and the daemon runs cycles in single-instance mode. The same
// config.toml works for VM (block disabled) and K8s (block enabled with
// values populated from the Downward API).
type K8sConfig struct {
	Enabled   bool   `toml:"enabled"`
	Namespace string `toml:"namespace"`
	PodName   string `toml:"pod_name"`
}

// RetentionConfig is the [retention] block.
type RetentionConfig struct {
	ClassificationsRetentionDays int `toml:"classifications_retention_days"`
	DiscoveredAPIsRetentionDays  int `toml:"discovered_apis_retention_days"`
}

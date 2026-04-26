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
	Deployment DeploymentConfig `toml:"deployment"`
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
	SSLMode               string `toml:"sslmode"` // disable | require | verify-ca | verify-full
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
	NormalizationRulesMeta NormRulesMetaConfig `toml:"normalization_rules_meta"`
	NormalizationRules     []NormalizationRule `toml:"normalization_rules"`
}

// NoiseFilterConfig holds [discovery.noise_filter].
type NoiseFilterConfig struct {
	PathPattern string   `toml:"path_pattern"`
	Ports       []int    `toml:"ports"`
	Domains     []string `toml:"domains"`
}

// NormRulesMetaConfig holds [discovery.normalization_rules_meta].
type NormRulesMetaConfig struct {
	Version string `toml:"version"`
}

// NormalizationRule is one [[discovery.normalization_rules]] entry.
//
// Compiled is populated by Validate() so the rule engine can reuse the
// pre-compiled regexp. It is not parsed from TOML.
type NormalizationRule struct {
	Name        string         `toml:"name"`
	Pattern     string         `toml:"pattern"`
	Placeholder string         `toml:"placeholder"`
	Compiled    *regexp.Regexp `toml:"-"`
}

// ManagedConfig is the [managed] block (Phase 2).
type ManagedConfig struct {
	PollIntervalMinutes int `toml:"poll_interval_minutes"`
	FetchConcurrency    int `toml:"fetch_concurrency"`
	DNSCacheTTLMinutes  int `toml:"dns_cache_ttl_minutes"`
}

// PollInterval returns the managed poll interval as a Duration.
func (m ManagedConfig) PollInterval() time.Duration {
	return time.Duration(m.PollIntervalMinutes) * time.Minute
}

// ComparisonConfig is the [comparison] block (Phase 3).
type ComparisonConfig struct {
	FreshnessThresholdMultiplier int `toml:"freshness_threshold_multiplier"`
}

// DeploymentConfig is the [deployment] block; nests topology.
type DeploymentConfig struct {
	Topology TopologyConfig `toml:"topology"`
}

// TopologyConfig is [deployment.topology].
type TopologyConfig struct {
	K8sNodes           []K8sNode           `toml:"k8s_nodes"`
	LegacyChosts       []string            `toml:"legacy_chosts"`
	NamespaceOverrides []NamespaceOverride `toml:"namespace_overrides"`
}

// K8sNode is one entry in [deployment.topology].k8s_nodes.
type K8sNode struct {
	IP               string `toml:"ip"`
	DefaultNamespace string `toml:"default_namespace"`
}

// NamespaceOverride is one [[deployment.topology.namespace_overrides]] entry.
type NamespaceOverride struct {
	APIID     string `toml:"api_id"`
	Namespace string `toml:"namespace"`
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
// bypassed and the daemon runs cycles in single-instance mode.
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

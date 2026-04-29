package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

var (
	validLogLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	validSSLModes  = map[string]bool{"disable": true, "require": true, "verify-ca": true, "verify-full": true}
)

// Validate checks the loaded Config for required fields, enum values, sane
// numeric ranges, and compiles every normalization regex. Errors accumulate;
// all problems are reported in one pass so the operator can fix them together.
func (c *Config) Validate() error {
	var errs []error

	// [ads]
	if c.ADS.Name == "" {
		errs = append(errs, errors.New("[ads] name: required"))
	}
	if c.ADS.LogLevel == "" {
		c.ADS.LogLevel = "info"
	} else if !validLogLevels[c.ADS.LogLevel] {
		errs = append(errs, fmt.Errorf("[ads] log_level: must be one of debug|info|warn|error, got %q", c.ADS.LogLevel))
	}

	// [database]
	if c.Database.Host == "" {
		errs = append(errs, errors.New("[database] host: required"))
	}
	if c.Database.Port <= 0 || c.Database.Port > 65535 {
		errs = append(errs, fmt.Errorf("[database] port: must be 1..65535, got %d", c.Database.Port))
	}
	if c.Database.Name == "" {
		errs = append(errs, errors.New("[database] name: required"))
	}
	if c.Database.User == "" {
		errs = append(errs, errors.New("[database] user: required"))
	}
	if c.Database.SSLMode != "" && !validSSLModes[c.Database.SSLMode] {
		errs = append(errs, fmt.Errorf("[database] sslmode: must be disable|require|verify-ca|verify-full, got %q", c.Database.SSLMode))
	}
	if c.Database.MaxOpenConns < 0 {
		errs = append(errs, errors.New("[database] max_open_conns: must be >= 0"))
	}
	if c.Database.MaxIdleConns < 0 {
		errs = append(errs, errors.New("[database] max_idle_conns: must be >= 0"))
	}

	// [deepflow]
	if c.DeepFlow.Enabled {
		if c.DeepFlow.ClickHouseURL == "" {
			errs = append(errs, errors.New("[deepflow] clickhouse_url: required when enabled=true"))
		} else if _, err := url.Parse(c.DeepFlow.ClickHouseURL); err != nil {
			errs = append(errs, fmt.Errorf("[deepflow] clickhouse_url: invalid URL: %w", err))
		}
		if c.DeepFlow.TimeoutSeconds <= 0 {
			errs = append(errs, errors.New("[deepflow] timeout_seconds: must be > 0"))
		}
	}

	// [apim]
	if c.APIM.PublisherURL == "" {
		errs = append(errs, errors.New("[apim] publisher_url: required"))
	} else if _, err := url.Parse(c.APIM.PublisherURL); err != nil {
		errs = append(errs, fmt.Errorf("[apim] publisher_url: invalid URL: %w", err))
	}
	if c.APIM.IntrospectURL == "" {
		errs = append(errs, errors.New("[apim] introspect_url: required (BFF needs it)"))
	}
	if c.APIM.TimeoutSeconds <= 0 {
		errs = append(errs, errors.New("[apim] timeout_seconds: must be > 0"))
	}

	// [discovery]
	if c.Discovery.PollIntervalMinutes <= 0 {
		errs = append(errs, errors.New("[discovery] poll_interval_minutes: must be > 0"))
	}
	if c.Discovery.WindowMinutes <= 0 {
		errs = append(errs, errors.New("[discovery] window_minutes: must be > 0"))
	}
	if c.Discovery.StatusMin < 100 || c.Discovery.StatusMin > 599 {
		errs = append(errs, fmt.Errorf("[discovery] status_min: must be 100..599, got %d", c.Discovery.StatusMin))
	}
	if c.Discovery.StatusMax < 100 || c.Discovery.StatusMax > 600 || c.Discovery.StatusMax <= c.Discovery.StatusMin {
		errs = append(errs, fmt.Errorf("[discovery] status_max: must be 100..600 and > status_min, got %d", c.Discovery.StatusMax))
	}
	if c.Discovery.MinObservations <= 0 {
		errs = append(errs, errors.New("[discovery] min_observations: must be > 0"))
	}
	if c.Discovery.MaxSignaturesPerWindow <= 0 {
		errs = append(errs, errors.New("[discovery] max_signatures_per_window: must be > 0"))
	}

	// [discovery.noise_filter]
	for i, p := range c.Discovery.NoiseFilter.PathPatterns {
		if p == "" {
			errs = append(errs, fmt.Errorf("[discovery.noise_filter] path_patterns[%d]: empty entry not allowed", i))
		}
	}
	for i, p := range c.Discovery.NoiseFilter.PathExact {
		if p == "" {
			errs = append(errs, fmt.Errorf("[discovery.noise_filter] path_exact[%d]: empty entry not allowed", i))
		}
	}
	for _, p := range c.Discovery.NoiseFilter.ExcludedPorts {
		if p <= 0 || p > 65535 {
			errs = append(errs, fmt.Errorf("[discovery.noise_filter] excluded_ports: %d not in 1..65535", p))
		}
	}

	// [discovery.normalization] — compile every regex once and store on
	// the config for reuse by the normalizer.
	if errs2 := c.compileNormalizationPatterns(); len(errs2) > 0 {
		errs = append(errs, errs2...)
	}

	// [managed]
	if c.Managed.PollIntervalMinutes <= 0 {
		errs = append(errs, errors.New("[managed] poll_interval_minutes: must be > 0"))
	}
	if c.Managed.FetchConcurrency <= 0 {
		errs = append(errs, errors.New("[managed] fetch_concurrency: must be > 0"))
	}

	// [comparison]
	if c.Comparison.FreshnessThresholdMultiplier <= 0 {
		errs = append(errs, errors.New("[comparison] freshness_threshold_multiplier: must be > 0"))
	}

	// [bff]
	if c.BFF.ListenAddr == "" {
		errs = append(errs, errors.New("[bff] listen_addr: required"))
	} else if _, _, err := net.SplitHostPort(c.BFF.ListenAddr); err != nil {
		errs = append(errs, fmt.Errorf("[bff] listen_addr: %w", err))
	}
	if c.BFF.ReadTimeoutSeconds <= 0 {
		errs = append(errs, errors.New("[bff] read_timeout_seconds: must be > 0"))
	}
	if c.BFF.WriteTimeoutSeconds <= 0 {
		errs = append(errs, errors.New("[bff] write_timeout_seconds: must be > 0"))
	}
	if c.BFF.TokenCache.TTLSeconds <= 0 {
		errs = append(errs, errors.New("[bff.token_cache] ttl_seconds: must be > 0"))
	}
	if c.BFF.TokenCache.MaxEntries <= 0 {
		errs = append(errs, errors.New("[bff.token_cache] max_entries: must be > 0"))
	}

	// [health]
	if c.Health.ListenAddr == "" {
		errs = append(errs, errors.New("[health] listen_addr: required"))
	} else if _, _, err := net.SplitHostPort(c.Health.ListenAddr); err != nil {
		errs = append(errs, fmt.Errorf("[health] listen_addr: %w", err))
	}

	// [retention]
	if c.Retention.ClassificationsRetentionDays <= 0 {
		errs = append(errs, errors.New("[retention] classifications_retention_days: must be > 0"))
	}
	if c.Retention.DiscoveredAPIsRetentionDays <= 0 {
		errs = append(errs, errors.New("[retention] discovered_apis_retention_days: must be > 0"))
	}

	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// compileNormalizationPatterns compiles every entry in builtin_patterns,
// user_patterns, and exclude_patterns. Returns one error per bad pattern
// and stores the compiled regex objects back on the config for reuse.
func (c *Config) compileNormalizationPatterns() []error {
	var errs []error
	n := &c.Discovery.Normalization

	compileList := func(list []string, label string) []*regexp.Regexp {
		out := make([]*regexp.Regexp, 0, len(list))
		for i, pat := range list {
			if pat == "" {
				errs = append(errs, fmt.Errorf("[discovery.normalization] %s[%d]: empty pattern not allowed", label, i))
				continue
			}
			compiled, err := regexp.Compile(pat)
			if err != nil {
				errs = append(errs, fmt.Errorf("[discovery.normalization] %s[%d] %q: invalid regex: %w", label, i, pat, err))
				continue
			}
			out = append(out, compiled)
		}
		return out
	}

	n.CompiledBuiltin = compileList(n.BuiltinPatterns, "builtin_patterns")
	n.CompiledUser = compileList(n.UserPatterns, "user_patterns")
	n.CompiledExclude = compileList(n.ExcludePatterns, "exclude_patterns")

	// version_pattern: must compile if present, and should also appear in
	// exclude_patterns (otherwise versions like "1.0.0" get normalized to
	// "{id}", which wrecks path matching).
	if n.VersionPattern != "" {
		if _, err := regexp.Compile(n.VersionPattern); err != nil {
			errs = append(errs, fmt.Errorf("[discovery.normalization] version_pattern %q: invalid regex: %w", n.VersionPattern, err))
		} else {
			found := false
			for _, p := range n.ExcludePatterns {
				if p == n.VersionPattern {
					found = true
					break
				}
			}
			if !found {
				errs = append(errs, fmt.Errorf("[discovery.normalization] version_pattern is set but the same regex is not in exclude_patterns; version segments will be normalized to {id}"))
			}
		}
	}

	return errs
}

// DSN builds a libpq-compatible Postgres connection string from the Database
// config. Useful for pgxpool.New().
func (d DatabaseConfig) DSN() string {
	parts := []string{
		fmt.Sprintf("host=%s", d.Host),
		fmt.Sprintf("port=%d", d.Port),
		fmt.Sprintf("dbname=%s", d.Name),
		fmt.Sprintf("user=%s", d.User),
	}
	if d.Password != "" {
		parts = append(parts, fmt.Sprintf("password=%s", d.Password))
	}
	if d.SSLMode != "" {
		parts = append(parts, fmt.Sprintf("sslmode=%s", d.SSLMode))
	}
	if d.ConnectTimeoutSeconds > 0 {
		parts = append(parts, fmt.Sprintf("connect_timeout=%d", d.ConnectTimeoutSeconds))
	}
	if d.MaxOpenConns > 0 {
		parts = append(parts, fmt.Sprintf("pool_max_conns=%d", d.MaxOpenConns))
	}
	return strings.Join(parts, " ")
}

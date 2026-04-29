package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalValidTOML returns a TOML payload that passes Validate(). Tests then
// mutate slices/fields on the parsed Config to exercise specific failure modes.
func minimalValidTOML() string {
	return `
[ads]
name = "ads"
log_level = "info"

[database]
host = "localhost"
port = 5432
name = "ads"
user = "ads"
password = "x"
sslmode = "disable"
max_open_conns = 25
max_idle_conns = 5
connect_timeout_seconds = 10

[deepflow]
enabled = true
clickhouse_url = "http://deepflow:9000"
clickhouse_user = "default"
clickhouse_password = "x"
verify_ssl = true
timeout_seconds = 30

[apim]
publisher_url = "https://apim:9443"
service_account_username = "ads"
service_account_password = "x"
verify_ssl = true
timeout_seconds = 30
introspect_url = "https://apim:9443/oauth2/introspect"
introspect_basic_auth = "x"

[discovery]
poll_interval_minutes = 5
window_minutes = 5
status_min = 200
status_max = 400
skip_internal = false
min_observations = 1
max_signatures_per_window = 10000

[discovery.noise_filter]
path_patterns = ["/health", "/metrics"]
path_exact = ["/", "/version"]
excluded_ports = [9090]
excluded_domains = ["k.svc"]

[discovery.normalization]
version_pattern = "^v?[0-9]+\\.[0-9]+(\\.[0-9]+)?$"
builtin_patterns = ["^[0-9]+$"]
user_patterns = []
exclude_patterns = ["^v?[0-9]+\\.[0-9]+(\\.[0-9]+)?$"]

[managed]
poll_interval_minutes = 10
fetch_concurrency = 5

[comparison]
freshness_threshold_multiplier = 3

[bff]
listen_addr = "0.0.0.0:8443"
tls_cert = "/etc/ads/cert"
tls_key = "/etc/ads/key"
verify_client_cert = false
read_timeout_seconds = 30
write_timeout_seconds = 30

[bff.token_cache]
ttl_seconds = 30
max_entries = 1000

[health]
listen_addr = "0.0.0.0:9090"

[retention]
classifications_retention_days = 90
discovered_apis_retention_days = 30
`
}

// writeTempConfig writes contents to a temp file and returns its path.
func writeTempConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return p
}

func TestLoadValidates(t *testing.T) {
	t.Setenv("TEST_DB_PASS", "from-env")
	toml := strings.Replace(minimalValidTOML(), `password = "x"`, `password = "${TEST_DB_PASS}"`, 1)

	cfg, err := Load(writeTempConfig(t, toml))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Database.Password != "from-env" {
		t.Errorf("expected env-expanded password 'from-env', got %q", cfg.Database.Password)
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate on minimal-valid: %v", err)
	}
	// Validate should populate the compiled regex slices.
	if len(cfg.Discovery.Normalization.CompiledBuiltin) != 1 {
		t.Errorf("Validate did not compile builtin patterns: got %d", len(cfg.Discovery.Normalization.CompiledBuiltin))
	}
	if len(cfg.Discovery.Normalization.CompiledExclude) != 1 {
		t.Errorf("Validate did not compile exclude patterns: got %d", len(cfg.Discovery.Normalization.CompiledExclude))
	}
}

func TestLoadFileMissing(t *testing.T) {
	if _, err := Load("/nonexistent/path/cfg.toml"); err == nil {
		t.Fatal("Load should error on missing file")
	}
}

func TestLoadParseError(t *testing.T) {
	bad := "this is = = invalid toml"
	if _, err := Load(writeTempConfig(t, bad)); err == nil {
		t.Fatal("Load should error on bad TOML")
	}
}

func TestValidateDetectsErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string // substring of the error
	}{
		{"missing db host", func(c *Config) { c.Database.Host = "" }, "[database] host"},
		{"bad db port", func(c *Config) { c.Database.Port = 0 }, "[database] port"},
		{"bad sslmode", func(c *Config) { c.Database.SSLMode = "weird" }, "sslmode"},
		{"bad log level", func(c *Config) { c.ADS.LogLevel = "verbose" }, "log_level"},
		{"missing apim publisher_url", func(c *Config) { c.APIM.PublisherURL = "" }, "publisher_url"},
		{"discovery status_max <= status_min", func(c *Config) { c.Discovery.StatusMax = 200 }, "status_max"},
		{"bad builtin regex", func(c *Config) { c.Discovery.Normalization.BuiltinPatterns[0] = "[invalid" }, "builtin_patterns"},
		{"bad exclude regex", func(c *Config) { c.Discovery.Normalization.ExcludePatterns[0] = "[invalid" }, "exclude_patterns"},
		{"empty noise pattern", func(c *Config) { c.Discovery.NoiseFilter.PathPatterns = []string{""} }, "path_patterns"},
		{"empty noise exact", func(c *Config) { c.Discovery.NoiseFilter.PathExact = []string{""} }, "path_exact"},
		{"bff bad listen addr", func(c *Config) { c.BFF.ListenAddr = "no_port" }, "[bff] listen_addr"},
		{"bff zero ttl", func(c *Config) { c.BFF.TokenCache.TTLSeconds = 0 }, "ttl_seconds"},
		{"bad health addr", func(c *Config) { c.Health.ListenAddr = "" }, "[health] listen_addr"},
		{"bad retention", func(c *Config) { c.Retention.ClassificationsRetentionDays = 0 }, "classifications_retention_days"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeTempConfig(t, minimalValidTOML()))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			tc.mutate(cfg)
			err = cfg.Validate()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want error containing %q, got %q", tc.want, err.Error())
			}
		})
	}
}

func TestDSN(t *testing.T) {
	d := DatabaseConfig{Host: "h", Port: 5432, Name: "n", User: "u", Password: "p", SSLMode: "require", ConnectTimeoutSeconds: 5, MaxOpenConns: 10}
	got := d.DSN()
	for _, want := range []string{"host=h", "port=5432", "dbname=n", "user=u", "password=p", "sslmode=require", "connect_timeout=5", "pool_max_conns=10"} {
		if !strings.Contains(got, want) {
			t.Errorf("DSN missing %q: %s", want, got)
		}
	}
}

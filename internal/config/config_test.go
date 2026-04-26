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
skip_internal = true
min_observations = 1
max_signatures_per_window = 10000

[discovery.noise_filter]
path_pattern = "^/(health|metrics)$"
ports = [9090]
domains = ["k.svc"]

[discovery.normalization_rules_meta]
version = "v1"

[[discovery.normalization_rules]]
name = "numeric_id"
pattern = '/[0-9]+\b'
placeholder = '/{id}'

[managed]
poll_interval_minutes = 10
fetch_concurrency = 5
dns_cache_ttl_minutes = 5

[comparison]
freshness_threshold_multiplier = 3

[deployment.topology]
k8s_nodes = [{ ip = "10.50.1.10", default_namespace = "techmart" }]
legacy_chosts = ["10.50.1.11"]

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
	// Validate should compile the regex onto the rule.
	if cfg.Discovery.NormalizationRules[0].Compiled == nil {
		t.Error("Validate did not compile normalization regex")
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
		{"bad noise regex", func(c *Config) { c.Discovery.NoiseFilter.PathPattern = "[invalid" }, "noise_filter"},
		{"bad normalization regex", func(c *Config) { c.Discovery.NormalizationRules[0].Pattern = "[invalid" }, "normalization_rules"},
		{"bad k8s node ip", func(c *Config) { c.Deployment.Topology.K8sNodes[0].IP = "not.an.ip" }, "k8s_nodes"},
		{"bad legacy chost ip", func(c *Config) { c.Deployment.Topology.LegacyChosts[0] = "x.y.z" }, "legacy_chosts"},
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

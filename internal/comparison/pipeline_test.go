package comparison

import (
	"testing"
	"time"

	"github.com/wso2/api-discovery-server/internal/config"
)

func TestFreshnessReject(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		ts        time.Time
		threshold time.Duration
		wantOK    bool
		wantHint  string
	}{
		{"fresh state", now.Add(-30 * time.Second), 30 * time.Minute, true, ""},
		{"borderline within", now.Add(-29 * time.Minute), 30 * time.Minute, true, ""},
		{"exactly at threshold passes", now.Add(-30 * time.Minute), 30 * time.Minute, true, ""},
		{"just past threshold rejected", now.Add(-31 * time.Minute), 30 * time.Minute, false, "stale"},
		{"never run rejected", time.Time{}, 30 * time.Minute, false, "never succeeded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, ok := freshnessReject(tc.ts, tc.threshold, now)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v (reason=%q)", ok, tc.wantOK, reason)
			}
			if tc.wantHint != "" && !contains(reason, tc.wantHint) {
				t.Errorf("reason = %q, want substring %q", reason, tc.wantHint)
			}
		})
	}
}

func TestFreshnessThreshold(t *testing.T) {
	cfg := &config.Config{
		Comparison: config.ComparisonConfig{FreshnessThresholdMultiplier: 3},
		Managed:    config.ManagedConfig{PollIntervalMinutes: 10},
	}
	got := freshnessThreshold(cfg)
	want := 30 * time.Minute
	if got != want {
		t.Errorf("threshold = %v, want %v", got, want)
	}
}

// contains is a tiny inline copy so we don't bring strings in for one call.
func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

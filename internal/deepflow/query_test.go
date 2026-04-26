package deepflow

import (
	"strings"
	"testing"
	"time"
)

func TestBuildPerFlowSQLContainsRequiredClauses(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	sql := BuildPerFlowSQL(PerFlowQuery{
		WindowStart:     now.Add(-5 * time.Minute),
		WindowEnd:       now,
		StatusMin:       200,
		StatusMax:       400,
		NoisePorts:      []int{9090, 6443},
		NoiseDomains:    []string{"kubernetes.default.svc"},
		MinObservations: 1,
		MaxSignatures:   10000,
	})

	for _, want := range []string{
		"FROM l7_flow_log",
		`l7_protocol_str IN ("HTTP","HTTP2")`,
		`observation_point IN ("s","s-p","c","c-p")`,
		"GROUP BY request_type, endpoint, observation_point, server_port, agent_id",
		"ORDER BY row_count DESC",
		"LIMIT 10000",
		"response_code >= 200",
		"response_code <  400",
		"server_port NOT IN (9090, 6443)",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("query missing %q\nfull SQL:\n%s", want, sql)
		}
	}

	// match() must NOT be present — the querier rejects it.
	if strings.Contains(sql, "match(") {
		t.Errorf("SQL contains match() which the DeepFlow querier rejects:\n%s", sql)
	}
	// request_domain NOT IN must NOT be present — the querier conflates
	// the SELECT alias with the WHERE column and errors with "Aggregate
	// function found in WHERE". Domain filtering is Go-side.
	if strings.Contains(sql, "request_domain NOT IN") {
		t.Errorf("SQL contains request_domain WHERE clause which the querier rejects:\n%s", sql)
	}
}

func TestBuildPerFlowSQLOmitsEmptyNoiseClauses(t *testing.T) {
	sql := BuildPerFlowSQL(PerFlowQuery{
		WindowStart:   time.Now().Add(-time.Minute),
		WindowEnd:     time.Now(),
		StatusMin:     200,
		StatusMax:     400,
		MaxSignatures: 100,
	})
	for _, mustNotContain := range []string{
		"NOT match(endpoint",
		"server_port NOT IN",
	} {
		if strings.Contains(sql, mustNotContain) {
			t.Errorf("expected no %q clause when noise filter empty:\n%s", mustNotContain, sql)
		}
	}
}

func TestQuoteString(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain", `"plain"`},
		{`with"quote`, `"with\"quote"`},
		{`with\back`, `"with\\back"`},
	}
	for _, tc := range cases {
		if got := quoteString(tc.in); got != tc.want {
			t.Errorf("quoteString(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

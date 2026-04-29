package deepflow

import (
	"fmt"
	"strings"
	"time"
)

// PerFlowQuery is the parameter bundle for BuildPerFlowSQL. Field names match
// the corresponding [discovery] config keys 1:1 so the call site can pass them
// straight from cfg.
//
// Note: NoisePathRegex is intentionally NOT included here — the DeepFlow
// querier does not accept ClickHouse's match() function in WHERE clauses
// (rejected by its SQL parser). The noise-path filter is applied Go-side
// in discovery.Pipeline after the query returns.
type PerFlowQuery struct {
	WindowStart     time.Time
	WindowEnd       time.Time
	StatusMin       int
	StatusMax       int
	NoisePorts      []int
	NoiseDomains    []string
	SkipInternal    bool
	MinObservations int
	MaxSignatures   int
}

// BuildPerFlowSQL returns the SELECT statement Phase 1 sends to the DeepFlow
// querier each cycle.
//
// Why the deviation from spec §3:
//   - The spec uses a CTE (WITH per_flow AS ...) which the querier rewrites
//     in a way that loses the `flow_log.` database prefix and queries
//     `default.l7_flow_log` instead. CTEs are therefore unsafe.
//   - The spec joins explicit flow_tag tables for K8s identity. The querier
//     auto-translates auto-tag columns (pod_service_1 etc.) into equivalent
//     dictGet calls, so we read those columns directly.
//   - The spec computes env_kind, service_identity, and traffic_direction in
//     SQL via multiIf branches. The querier rejects those compositions; we
//     instead return the raw signal columns and let the discovery package's
//     Classify() build the same truth tables in Go.
//
// Inputs that need string interpolation (regex, ports, domains, time bounds)
// are placed only in WHERE-clause literals — there's no end-user input on
// this path. SQL injection risk is zero because the inputs come from
// config.toml at startup, not from runtime requests.
func BuildPerFlowSQL(p PerFlowQuery) string {
	var b strings.Builder
	b.WriteString(`SELECT
    request_type,
    endpoint,
    observation_point,
    server_port,
    agent_id,
    ip4_0 AS client_ip,
    Count(row) AS row_count,
    any(request_resource) AS sample_url,
    any(request_domain) AS request_domain,
    any(response_code) AS sample_status,
    any(pod_service_1) AS k8s_service,
    any(pod_ns_1) AS k8s_namespace,
    any(pod_group_1) AS k8s_workload,
    any(pod_1) AS k8s_pod,
    any(ip4_1) AS server_ip,
    any(auto_instance_type_1) AS instance_type_server,
    any(auto_instance_type_0) AS instance_type_client,
    any(pod_ns_0) AS client_namespace,
    any(pod_group_0) AS client_workload,
    any(pod_0) AS client_pod,
    any(client_port) AS client_port_sample,
    toUnixTimestamp(Min(start_time)) AS first_seen_unix,
    toUnixTimestamp(Max(end_time)) AS last_seen_unix,
    Avg(response_duration) AS avg_duration_us
FROM l7_flow_log
WHERE protocol = 6
  AND l7_protocol_str IN ("HTTP","HTTP2")
  AND observation_point IN ("s","s-p","c","c-p")
`)
	fmt.Fprintf(&b, "  AND toUnixTimestamp(start_time) >= %d\n", p.WindowStart.Unix())
	fmt.Fprintf(&b, "  AND toUnixTimestamp(start_time) <  %d\n", p.WindowEnd.Unix())
	fmt.Fprintf(&b, "  AND response_code >= %d\n", p.StatusMin)
	fmt.Fprintf(&b, "  AND response_code <  %d\n", p.StatusMax)
	b.WriteString(`  AND endpoint != ""
  AND request_type != ""
`)

	// Noise filters: only server_port goes in SQL — the querier accepts
	// NOT IN with integer lists cleanly. Two filters live Go-side instead:
	//   - path-regex: rejected because the querier disallows match()
	//   - request_domain: rejected because the querier auto-aliases our
	//     SELECT `any(request_domain) AS request_domain` and re-applies the
	//     aggregate inside the WHERE rewrite, producing
	//     "Aggregate function found in WHERE".
	// Both are applied in discovery.Pipeline after the query returns.
	if len(p.NoisePorts) > 0 {
		fmt.Fprintf(&b, "  AND server_port NOT IN (%s)\n", joinInts(p.NoisePorts))
	}

	b.WriteString(`GROUP BY request_type, endpoint, observation_point, server_port, agent_id, ip4_0
`)

	// Cap result size as the last clause so the rest of the WHERE clause
	// applies first (querier respects ORDER BY before LIMIT).
	if p.MinObservations > 1 {
		fmt.Fprintf(&b, "HAVING row_count >= %d\n", p.MinObservations)
	}
	b.WriteString("ORDER BY row_count DESC\n")
	if p.MaxSignatures > 0 {
		fmt.Fprintf(&b, "LIMIT %d", p.MaxSignatures)
	}

	return b.String()
}

// quoteString returns s wrapped in double quotes with internal double quotes
// escaped. The querier requires double quotes for string literals and
// rejects single-quoted strings.
func quoteString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// joinInts produces the body of an SQL IN list from a slice of ints.
func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprintf("%d", x)
	}
	return strings.Join(parts, ", ")
}

// joinStrings produces the body of an SQL IN list from a slice of strings.
func joinStrings(xs []string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = quoteString(x)
	}
	return strings.Join(parts, ", ")
}

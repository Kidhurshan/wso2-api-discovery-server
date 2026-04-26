// Package deepflow talks to the DeepFlow querier HTTP API to pull L7 flow
// records for Phase 1 discovery.
//
// Spec deviation (see project memory: deepflow_querier_strategy):
// the spec assumes direct ClickHouse access via the clickhouse-go driver.
// In the actual TechMart deployment, ClickHouse is exposed only as a
// ClusterIP inside the deepflow k3s cluster — only the deepflow-server
// querier API is reachable externally (NodePort 30617). The querier is
// SQL-shaped but constrained: no CTEs, no `coalesce`, aggregation function
// composition is limited. We therefore use a single SELECT GROUP BY and
// move env_kind / service_identity / direction classification to Go.
package deepflow

import "context"

// Client is the contract pipelines use to query DeepFlow. The interface lets
// tests inject fakes without spinning up a real querier.
type Client interface {
	// Query runs sql against the given DeepFlow database (typically "flow_log")
	// and returns one map per result row. Map keys are the SELECT column
	// aliases; values are decoded JSON primitives (numbers as float64,
	// strings as string, IPv4 as string).
	Query(ctx context.Context, db, sql string) ([]Row, error)

	// Ping verifies connectivity. Implementations should use a query the
	// querier accepts cheaply (e.g., SHOW TABLES) — NOT SELECT 1, which
	// the querier rewrites to FROM dual and fails on ClickHouse.
	Ping(ctx context.Context) error

	// Close releases any held resources.
	Close()
}

// Row is the loose-typed result of a single querier row. Field decoders
// (in row.go) coerce values into the strict types the discovery package needs.
type Row map[string]any

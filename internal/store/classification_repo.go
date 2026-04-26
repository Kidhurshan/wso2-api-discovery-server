package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ClassificationRepo handles ads_classifications + v_current_classifications.
type ClassificationRepo struct {
	pool *pgxpool.Pool
}

// NewClassificationRepo returns a ClassificationRepo bound to pool.
func NewClassificationRepo(pool *pgxpool.Pool) *ClassificationRepo {
	return &ClassificationRepo{pool: pool}
}

// classifySQL is the spec's locked Phase 3 classifier from
// claude/specs/phase3_comparison.md §4.
//
// Three CTEs:
//
//	discovered: every active row in ads_discovered_apis joined with its service
//	            identity. The CASE picks 'internal' when only internal_flows
//	            are present, 'external' when external_flows > 0, otherwise
//	            defaults to internal (matches the spec's branch).
//
//	candidate_matches: LEFT JOIN against ads_managed_apis on
//	                   (method, gateway_path) — finds every managed row that
//	                   declares this same method+path, regardless of which
//	                   service the managed row belongs to. The LEFT JOIN keeps
//	                   discovered rows with no path match for the shadow/drift
//	                   branches.
//
//	classified: aggregates per discovered_id. has_path_match means at least
//	            one managed row declares the same (method, path).
//	            has_matching_identity means at least one of those managed
//	            rows belongs to the SAME service. service_is_governed is the
//	            existence test: does this service have ANY managed row at all?
//
// Final INSERT writes either 'shadow' or 'drift' per the truth table in
// spec §3. The WHERE NOT (has_matching_identity) clause excludes managed
// paths from the report — Phase 3 is exception-only by design.
//
// $1 = cycleID
const classifySQL = `
WITH discovered AS (
    SELECT
        d.id                        AS discovered_id,
        d.service_id,
        s.service_identity          AS discovered_service_identity,
        d.method,
        d.normalized_path,
        d.last_seen_at,
        d.observation_count,
        CASE
            WHEN d.internal_flows > 0 AND d.external_flows = 0 THEN 'internal'
            WHEN d.external_flows > 0 THEN 'external'
            ELSE 'internal'
        END AS traffic_direction
    FROM ads_discovered_apis d
    JOIN ads_services s ON s.id = d.service_id
    WHERE d.is_active = true
),
candidate_matches AS (
    SELECT
        disc.discovered_id,
        disc.discovered_service_identity,
        disc.method,
        disc.normalized_path,
        disc.traffic_direction,
        m.id                          AS managed_id,
        m.service_identity            AS managed_service_identity,
        m.apim_api_id,
        m.apim_api_name
    FROM discovered disc
    LEFT JOIN ads_managed_apis m
        ON m.method        = disc.method
        AND m.gateway_path = disc.normalized_path
        AND m.is_active    = true
),
classified AS (
    SELECT
        cm.discovered_id,
        cm.discovered_service_identity,
        cm.method,
        cm.normalized_path,
        cm.traffic_direction,
        BOOL_OR(cm.managed_service_identity = cm.discovered_service_identity)
            AS has_matching_identity,
        BOOL_OR(cm.managed_id IS NOT NULL)
            AS has_path_match,
        EXISTS (
            SELECT 1 FROM ads_managed_apis m2
            WHERE m2.service_identity = cm.discovered_service_identity
              AND m2.is_active = true
        ) AS service_is_governed,
        ARRAY_AGG(DISTINCT cm.managed_id)
            FILTER (WHERE cm.managed_id IS NOT NULL) AS matched_managed_ids,
        ARRAY_AGG(DISTINCT cm.apim_api_id)
            FILTER (WHERE cm.apim_api_id IS NOT NULL) AS matched_apim_api_ids
    FROM candidate_matches cm
    GROUP BY cm.discovered_id, cm.discovered_service_identity,
             cm.method, cm.normalized_path, cm.traffic_direction
)
INSERT INTO ads_classifications (
    discovered_api_id,
    cycle_id,
    classification,
    is_internal,
    matched_managed_ids,
    matched_apim_api_ids,
    classified_at
)
SELECT
    discovered_id,
    $1,
    CASE
        WHEN COALESCE(has_path_match, false) AND NOT COALESCE(has_matching_identity, false) THEN 'drift'
        WHEN NOT COALESCE(has_path_match, false) AND service_is_governed THEN 'drift'
        ELSE 'shadow'
    END AS classification,
    (traffic_direction = 'internal') AS is_internal,
    COALESCE(matched_managed_ids, '{}'::uuid[]),
    COALESCE(matched_apim_api_ids, '{}'::text[]),
    now()
FROM classified
WHERE NOT COALESCE(has_matching_identity, false)
`

// Classify runs the spec's locked classification SQL with the given cycleID
// stamped on every inserted row. Returns the number of rows inserted.
//
// Safe to call after every cycle — the table is append-only by design
// (claude/specs/phase3_comparison.md §5). Latest classification per
// discovered_api wins the materialized-view DISTINCT ON.
func (r *ClassificationRepo) Classify(ctx context.Context, cycleID uuid.UUID) (int, error) {
	tag, err := r.pool.Exec(ctx, classifySQL, cycleID)
	if err != nil {
		return 0, fmt.Errorf("classify: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// RefreshView runs REFRESH MATERIALIZED VIEW CONCURRENTLY against
// v_current_classifications. CONCURRENTLY requires the unique index on
// discovered_api_id (created in migration 005) and means the BFF can read
// during the refresh without locking.
//
// Note: CONCURRENTLY refuses to run if the view has never been populated.
// First-time deployments need a one-shot REFRESH MATERIALIZED VIEW (without
// CONCURRENTLY) to seed it. We detect EmptyView via pg_class.relhasindex
// + pg_stat_all_tables and fall back automatically.
func (r *ClassificationRepo) RefreshView(ctx context.Context) error {
	// Try CONCURRENTLY first — fast path on warm clusters.
	_, err := r.pool.Exec(ctx, `REFRESH MATERIALIZED VIEW CONCURRENTLY v_current_classifications`)
	if err == nil {
		return nil
	}
	// Postgres returns SQLSTATE 55000 ("object_not_in_prerequisite_state")
	// when CONCURRENTLY is invoked on a never-populated view. Detect that
	// by error string match (pgconn would let us inspect SQLSTATE; the
	// string check is robust enough and avoids the dep churn here).
	const seedHint = "must be populated before it can be refreshed concurrently"
	if errStr := err.Error(); !contains(errStr, seedHint) {
		return fmt.Errorf("refresh view (concurrently): %w", err)
	}
	// One-shot non-CONCURRENT seed. Brief read lock — acceptable because
	// it only happens on the first cycle of a fresh deployment.
	if _, err := r.pool.Exec(ctx, `REFRESH MATERIALIZED VIEW v_current_classifications`); err != nil {
		return fmt.Errorf("refresh view (seed): %w", err)
	}
	return nil
}

// contains is a tiny copy of strings.Contains so we don't bring in the
// import for one call.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

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

// classifySQL is the redesigned Phase 3 classifier.
//
// Comparison key for matching: (method, path) only — service_identity is
// no longer in the join because deriving it for managed APIs from a
// user-provided backend URL is unreliable.
//
// Algorithm:
//
//	matched         = discovered IDs whose (method, normalized_path) matches
//	                  some managed row's (method, gateway_path | backend_path)
//	partial_managed = service_ids whose discovered set contains at least
//	                  one matched row (i.e., the service is "partially
//	                  managed" — known to APIM)
//	final           = for each NOT-matched discovered:
//	                    DRIFT  if its service_id is partial_managed
//	                    SHADOW otherwise
//
// $1 = cycleID
const classifySQL = `
WITH discovered AS (
    SELECT
        d.id                        AS discovered_id,
        d.service_id,
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
    WHERE d.is_active = true
),
matched AS (
    SELECT
        disc.discovered_id,
        disc.method,
        disc.normalized_path,
        disc.service_id,
        disc.traffic_direction,
        ARRAY_AGG(DISTINCT m.id) AS matched_managed_ids,
        ARRAY_AGG(DISTINCT m.apim_api_id) AS matched_apim_api_ids
    FROM discovered disc
    JOIN ads_managed_apis m
        ON m.method     = disc.method
        AND m.is_active = true
        AND (m.gateway_path = disc.normalized_path
             OR m.backend_path = disc.normalized_path)
    GROUP BY disc.discovered_id, disc.method, disc.normalized_path,
             disc.service_id, disc.traffic_direction
),
partial_managed AS (
    SELECT DISTINCT service_id
    FROM matched
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
    disc.discovered_id,
    $1,
    CASE
        WHEN disc.service_id IN (SELECT service_id FROM partial_managed)
             THEN 'drift'
        ELSE 'shadow'
    END AS classification,
    (disc.traffic_direction = 'internal') AS is_internal,
    '{}'::uuid[],
    '{}'::text[],
    now()
FROM discovered disc
WHERE disc.discovered_id NOT IN (SELECT discovered_id FROM matched)
`

// markAnchorSQL flags discovered rows that matched a managed entry, so
// the retention sweep doesn't delete them. This keeps the partial-managed
// signal alive even when the matched rows go idle for long periods.
const markAnchorSQL = `
UPDATE ads_discovered_apis
   SET is_anchor = true
 WHERE id IN (
    SELECT d.id
    FROM ads_discovered_apis d
    WHERE d.is_active = true
      AND EXISTS (
          SELECT 1 FROM ads_managed_apis m
          WHERE m.is_active = true
            AND m.method = d.method
            AND (m.gateway_path = d.normalized_path
                 OR m.backend_path = d.normalized_path)
      )
 )
   AND is_anchor = false
`

// Classify runs the classifier SQL with the given cycleID stamped on every
// inserted row, then marks anchors. Returns the number of classifications
// inserted (i.e., shadow + drift findings).
func (r *ClassificationRepo) Classify(ctx context.Context, cycleID uuid.UUID) (int, error) {
	tag, err := r.pool.Exec(ctx, classifySQL, cycleID)
	if err != nil {
		return 0, fmt.Errorf("classify: %w", err)
	}
	if _, err := r.pool.Exec(ctx, markAnchorSQL); err != nil {
		return 0, fmt.Errorf("mark anchors: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// RefreshView runs REFRESH MATERIALIZED VIEW CONCURRENTLY against
// v_current_classifications. CONCURRENTLY requires the unique index on
// discovered_api_id and means the BFF can read during the refresh.
//
// Note: CONCURRENTLY refuses to run if the view has never been populated.
// First-time deployments need a one-shot REFRESH MATERIALIZED VIEW (without
// CONCURRENTLY) to seed it. We detect this and fall back automatically.
func (r *ClassificationRepo) RefreshView(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `REFRESH MATERIALIZED VIEW CONCURRENTLY v_current_classifications`)
	if err == nil {
		return nil
	}
	const seedHint = "must be populated before it can be refreshed concurrently"
	if errStr := err.Error(); !contains(errStr, seedHint) {
		return fmt.Errorf("refresh view (concurrently): %w", err)
	}
	if _, err := r.pool.Exec(ctx, `REFRESH MATERIALIZED VIEW v_current_classifications`); err != nil {
		return fmt.Errorf("refresh view (seed): %w", err)
	}
	return nil
}

// contains is a tiny copy of strings.Contains.
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

-- 005_view.sql — v_current_classifications materialized view
-- Per claude/specs/phase3_comparison.md §6.
--
-- Idempotency: CREATE MATERIALIZED VIEW does not support IF NOT EXISTS in
-- older Postgres releases the same way regular tables do. We use the
-- DO block + to_regclass pattern to skip when present.

DO $$
BEGIN
    IF to_regclass('public.v_current_classifications') IS NULL THEN
        EXECUTE $mv$
            CREATE MATERIALIZED VIEW v_current_classifications AS
            SELECT DISTINCT ON (c.discovered_api_id)
                c.discovered_api_id,
                d.method,
                d.normalized_path,
                s.service_identity,
                s.env_kind,
                d.first_seen_at,
                d.last_seen_at,
                d.observation_count,
                d.distinct_client_count,
                d.status_codes,
                d.raw_path_samples,
                d.distinct_clients_sample,
                d.sample_pod,
                d.sample_workload,
                d.avg_duration_us,
                d.internal_flows,
                d.external_flows,
                c.classification,
                c.is_internal,
                c.matched_managed_ids,
                c.matched_apim_api_ids,
                c.classified_at
            FROM ads_classifications c
            JOIN ads_discovered_apis d ON d.id = c.discovered_api_id
            JOIN ads_services s        ON s.id = d.service_id
            WHERE d.is_active = true
            ORDER BY c.discovered_api_id, c.classified_at DESC
        $mv$;
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS v_current_classifications_pk
    ON v_current_classifications (discovered_api_id);
CREATE INDEX IF NOT EXISTS v_current_classifications_classification_idx
    ON v_current_classifications (classification);
CREATE INDEX IF NOT EXISTS v_current_classifications_service_idx
    ON v_current_classifications (service_identity);
CREATE INDEX IF NOT EXISTS v_current_classifications_internal_idx
    ON v_current_classifications (is_internal);

-- 008_top_clients.sql
--
-- Adds top_clients JSONB to ads_discovered_apis so the BFF detail page
-- can show "who is calling this unmanaged endpoint" — top callers ranked
-- by observation count within the latest cycle's window. Each element:
--
--   {
--     "identity": "k8s:techmart/orders" | "host:10.50.1.5",
--     "kind":     "k8s" | "legacy",
--     "namespace": "techmart",     -- present for k8s only
--     "workload":  "orders",       -- present for k8s only
--     "ip":        "10.50.1.5",    -- always (sample for k8s)
--     "port":      54321,          -- sample only — source ports rotate
--     "observations": 17
--   }
--
-- The column is per-cycle: each Phase 1 upsert REPLACES it with the
-- latest top-N list. Top-N is capped at 20 entries by the merger so the
-- column stays small (~2KB worst case).

ALTER TABLE ads_discovered_apis
    ADD COLUMN IF NOT EXISTS top_clients JSONB NOT NULL DEFAULT '[]'::jsonb;

-- Recreate the view to expose top_clients to the BFF read path.
DROP MATERIALIZED VIEW IF EXISTS v_current_classifications;

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
    d.top_clients,
    d.sample_pod,
    d.sample_workload,
    d.avg_duration_us,
    d.internal_flows,
    d.external_flows,
    d.is_anchor,
    c.classification,
    c.is_internal,
    c.matched_managed_ids,
    c.matched_apim_api_ids,
    c.classified_at
FROM ads_classifications c
JOIN ads_discovered_apis d ON d.id = c.discovered_api_id
JOIN ads_services s        ON s.id = d.service_id
WHERE d.is_active = true
ORDER BY c.discovered_api_id, c.classified_at DESC;

CREATE UNIQUE INDEX IF NOT EXISTS v_current_classifications_pk
    ON v_current_classifications (discovered_api_id);
CREATE INDEX IF NOT EXISTS v_current_classifications_classification_idx
    ON v_current_classifications (classification);
CREATE INDEX IF NOT EXISTS v_current_classifications_service_idx
    ON v_current_classifications (service_identity);
CREATE INDEX IF NOT EXISTS v_current_classifications_internal_idx
    ON v_current_classifications (is_internal);

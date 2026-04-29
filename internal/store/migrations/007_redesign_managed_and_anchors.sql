-- 007_redesign_managed_and_anchors.sql
--
-- Phase 3 redesign per the design discussion: drop service_identity from
-- the matching key; match on (method, path) only; classify shadow/drift
-- by grouping the discovered side via service_identity.
--
-- This migration is destructive — it drops columns from ads_managed_apis
-- that the new comparison logic no longer uses, and adds the two paths
-- (gateway and backend) the new logic does use. After applying, Phase 2
-- repopulates the table on its next cycle (~10 min).
--
-- It also adds is_anchor on ads_discovered_apis so the retention sweep
-- can preserve rows that have ever matched a managed entry, which keeps
-- the partial-managed signal alive for low-traffic services.

-- ─── ads_managed_apis ─────────────────────────────────────────────────────

-- Drop the unique constraint that referenced gateway_path; we'll recreate
-- after restructuring.
ALTER TABLE ads_managed_apis DROP CONSTRAINT IF EXISTS ads_managed_apis_uniq;

-- Indexes that referenced columns we're about to drop.
DROP INDEX IF EXISTS ads_managed_apis_service_idx;
DROP INDEX IF EXISTS ads_managed_apis_match_idx;

-- Drop columns the new design no longer needs.
ALTER TABLE ads_managed_apis
    DROP COLUMN IF EXISTS env_kind,
    DROP COLUMN IF EXISTS service_identity,
    DROP COLUMN IF EXISTS backend_resolved_ip,
    DROP COLUMN IF EXISTS backend_resolved_port,
    DROP COLUMN IF EXISTS operation_target,
    DROP COLUMN IF EXISTS raw_operation_target,
    DROP COLUMN IF EXISTS raw_placeholders;

-- The old gateway_path column already exists. Rename keeps the data.
-- Add the new backend_path column. Defaults to '' so existing rows don't
-- break the NOT NULL constraint; Phase 2's next cycle will repopulate.
ALTER TABLE ads_managed_apis
    ADD COLUMN IF NOT EXISTS backend_path TEXT NOT NULL DEFAULT '';

-- Re-add the unique constraint using the new shape — same logical
-- uniqueness (one row per APIM operation) but on the new columns.
ALTER TABLE ads_managed_apis
    ADD CONSTRAINT ads_managed_apis_uniq UNIQUE (apim_api_id, method, gateway_path);

-- New index for the comparison join (matches on method + either path).
CREATE INDEX IF NOT EXISTS ads_managed_apis_match_idx
    ON ads_managed_apis (method, gateway_path, backend_path)
    WHERE is_active;

-- ─── ads_discovered_apis ──────────────────────────────────────────────────

-- Anchor flag: set true when Phase 3 finds this discovered row matched
-- a managed entry. Retention sweep skips anchor rows so the partial-
-- managed signal stays alive even when the service goes idle.
ALTER TABLE ads_discovered_apis
    ADD COLUMN IF NOT EXISTS is_anchor BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS ads_discovered_apis_anchor_idx
    ON ads_discovered_apis (is_anchor) WHERE is_anchor;

-- ─── v_current_classifications ────────────────────────────────────────────
-- Recreate the materialized view because we removed columns it referenced
-- (managed.service_identity) and the new classification logic puts
-- different fields in matched_managed_ids etc.

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

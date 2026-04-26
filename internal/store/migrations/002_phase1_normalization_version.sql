-- 002_phase1_normalization_version.sql — ads_discovered_apis
-- Per claude/specs/phase1_discovery.md §5.1.

CREATE TABLE IF NOT EXISTS ads_discovered_apis (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_id               UUID NOT NULL REFERENCES ads_services(id) ON DELETE CASCADE,
    method                   TEXT NOT NULL,
    normalized_path          TEXT NOT NULL,
    raw_path_samples         TEXT[] NOT NULL DEFAULT '{}',
    first_seen_at            TIMESTAMPTZ NOT NULL,
    last_seen_at             TIMESTAMPTZ NOT NULL,
    observation_count        BIGINT NOT NULL DEFAULT 0,
    flow_count               BIGINT NOT NULL DEFAULT 0,
    distinct_client_count    INTEGER NOT NULL DEFAULT 0,
    distinct_clients_sample  TEXT[] NOT NULL DEFAULT '{}',
    status_codes             SMALLINT[] NOT NULL DEFAULT '{}',
    avg_duration_us          DOUBLE PRECISION,
    request_domain           TEXT,
    internal_flows           BIGINT NOT NULL DEFAULT 0,
    external_flows           BIGINT NOT NULL DEFAULT 0,
    sample_pod               TEXT,
    sample_workload          TEXT,
    normalization_version    TEXT NOT NULL,
    last_window_id           UUID,
    is_active                BOOLEAN NOT NULL DEFAULT true,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ads_discovered_apis_uniq UNIQUE (service_id, method, normalized_path)
);

CREATE INDEX IF NOT EXISTS ads_discovered_apis_last_seen_idx
    ON ads_discovered_apis (last_seen_at DESC);
CREATE INDEX IF NOT EXISTS ads_discovered_apis_service_idx
    ON ads_discovered_apis (service_id);
CREATE INDEX IF NOT EXISTS ads_discovered_apis_active_idx
    ON ads_discovered_apis (is_active) WHERE is_active;

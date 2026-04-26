-- 003_managed_apis.sql — ads_managed_apis
-- Per claude/specs/phase2_managed_sync.md §7.

CREATE TABLE IF NOT EXISTS ads_managed_apis (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    apim_api_id              TEXT NOT NULL,
    apim_api_name            TEXT NOT NULL,
    apim_api_version         TEXT NOT NULL,
    apim_api_context         TEXT NOT NULL,
    apim_api_provider        TEXT,
    apim_lifecycle_status    TEXT NOT NULL,

    env_kind                 TEXT NOT NULL CHECK (env_kind IN ('k8s', 'legacy', 'unknown')),
    service_identity         TEXT NOT NULL,

    method                   TEXT NOT NULL,
    gateway_path             TEXT NOT NULL,
    operation_target         TEXT NOT NULL,
    raw_operation_target     TEXT NOT NULL DEFAULT '',
    raw_placeholders         TEXT[] NOT NULL DEFAULT '{}',
    auth_type                TEXT,
    throttling_policy        TEXT,

    backend_url              TEXT NOT NULL,
    backend_resolved_ip      TEXT,
    backend_resolved_port    INTEGER,

    apim_updated_time        TIMESTAMPTZ,
    last_synced_at           TIMESTAMPTZ NOT NULL,
    is_active                BOOLEAN NOT NULL DEFAULT true,
    warnings                 TEXT[] NOT NULL DEFAULT '{}',

    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT ads_managed_apis_uniq UNIQUE (apim_api_id, method, gateway_path)
);

CREATE INDEX IF NOT EXISTS ads_managed_apis_service_idx ON ads_managed_apis (service_identity);
CREATE INDEX IF NOT EXISTS ads_managed_apis_active_idx ON ads_managed_apis (is_active) WHERE is_active;
CREATE INDEX IF NOT EXISTS ads_managed_apis_match_idx ON ads_managed_apis (method, gateway_path) WHERE is_active;

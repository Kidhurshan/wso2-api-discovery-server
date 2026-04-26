-- 001_init.sql — extensions, ads_services, ads_pipeline_state
-- Per claude/specs/project_build.md §5.1.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS ads_services (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_identity    TEXT NOT NULL UNIQUE,
    env_kind            TEXT NOT NULL CHECK (env_kind IN ('k8s', 'legacy')),
    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    first_seen_at       TIMESTAMPTZ NOT NULL,
    last_seen_at        TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS ads_services_last_seen_idx
    ON ads_services (last_seen_at DESC);

CREATE TABLE IF NOT EXISTS ads_pipeline_state (
    id                                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phase1_last_success                 TIMESTAMPTZ,
    phase1_last_window_start            TIMESTAMPTZ,
    phase1_last_window_end              TIMESTAMPTZ,
    phase2_last_success                 TIMESTAMPTZ,
    phase3_last_success                 TIMESTAMPTZ,
    phase3_last_view_refresh            TIMESTAMPTZ,
    last_retention_run                  TIMESTAMPTZ,
    discovery_breaker_state             TEXT NOT NULL DEFAULT 'closed',
    managed_breaker_state               TEXT NOT NULL DEFAULT 'closed'
);

-- Seed the single state row only if the table is empty.
INSERT INTO ads_pipeline_state DEFAULT VALUES
    ON CONFLICT DO NOTHING;

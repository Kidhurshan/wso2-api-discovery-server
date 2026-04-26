-- 004_classifications.sql — ads_classifications (append-only)
-- Per claude/specs/phase3_comparison.md §5.1.

CREATE TABLE IF NOT EXISTS ads_classifications (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    discovered_api_id    UUID NOT NULL REFERENCES ads_discovered_apis(id) ON DELETE CASCADE,
    cycle_id             UUID NOT NULL,
    classification       TEXT NOT NULL CHECK (classification IN ('shadow', 'drift')),
    is_internal          BOOLEAN NOT NULL DEFAULT false,
    matched_managed_ids  UUID[] NOT NULL DEFAULT '{}',
    matched_apim_api_ids TEXT[] NOT NULL DEFAULT '{}',
    classified_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS ads_classifications_discovered_idx
    ON ads_classifications (discovered_api_id, classified_at DESC);
CREATE INDEX IF NOT EXISTS ads_classifications_cycle_idx
    ON ads_classifications (cycle_id);
CREATE INDEX IF NOT EXISTS ads_classifications_classification_idx
    ON ads_classifications (classification);

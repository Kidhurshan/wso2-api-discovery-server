-- 006_capped_array_union.sql — helper used by Phase 1 upsert
-- Per claude/specs/phase1_discovery.md §5.2.
--
-- CREATE OR REPLACE makes this idempotent on its own.

CREATE OR REPLACE FUNCTION ads_capped_array_union(a TEXT[], b TEXT[], cap INT)
RETURNS TEXT[] AS $$
    SELECT array_agg(DISTINCT v ORDER BY v)::TEXT[]
    FROM (SELECT unnest(a) AS v UNION SELECT unnest(b)) sub
    LIMIT cap;
$$ LANGUAGE SQL IMMUTABLE;

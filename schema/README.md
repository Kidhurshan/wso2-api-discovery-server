# Schema migrations

Files apply in lexicographic order. Each is idempotent (`CREATE TABLE IF NOT EXISTS`, `CREATE INDEX IF NOT EXISTS`). The daemon runs them at startup automatically; `make migrate-up` is for manual ops application.

| File | Round | Adds |
|---|---|---|
| `001_init.sql` | 1 | `pgcrypto` extension, `ads_services`, `ads_pipeline_state` |
| `002_phase1_normalization_version.sql` | 2 | `ads_discovered_apis` + indexes |
| `003_managed_apis.sql` | 3 | `ads_managed_apis` + indexes |
| `004_classifications.sql` | 4 | `ads_classifications` + indexes |
| `005_view.sql` | 4 | `v_current_classifications` materialized view + indexes |
| `006_capped_array_union.sql` | 2 | `ads_capped_array_union(text[], text[], int)` helper used by Phase 1 upsert |

Files are populated round-by-round per the plan. Round 1 lays down 001 + 002 + 006 (the foundation Phase 1 needs).

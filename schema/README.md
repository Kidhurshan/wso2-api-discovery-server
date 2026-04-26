# Schema migrations

> The canonical SQL files now live at [internal/store/migrations/](../internal/store/migrations/) so `//go:embed` can pick them up at build time. Go's embed directive cannot traverse `..`, so the migrations must live inside the package that embeds them. This `schema/` directory is kept as a documentation pointer only.

The daemon runs migrations at startup via `store.RunMigrations()`. For manual application use:

```bash
make migrate-up
```

(see [Makefile](../Makefile) — uses `psql $ADS_DB_URL`).

## File order

Files apply in lexicographic order. Each is idempotent.

| File | Round | Adds |
|---|---|---|
| `001_init.sql` | 1 | `pgcrypto`, `ads_services`, `ads_pipeline_state` (with seed row) |
| `002_phase1_normalization_version.sql` | 2 | `ads_discovered_apis` + indexes |
| `003_managed_apis.sql` | 3 | `ads_managed_apis` + indexes |
| `004_classifications.sql` | 4 | `ads_classifications` + indexes |
| `005_view.sql` | 4 | `v_current_classifications` materialized view |
| `006_capped_array_union.sql` | 1/2 | `ads_capped_array_union(text[], text[], int)` helper |

## Naming convention

`<3-digit-seq>_<short_topic>.sql`. New migrations append; never edit a committed migration in place (re-running it after a change would not pick up edits).

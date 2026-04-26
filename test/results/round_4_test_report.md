# Round 4 Test Report — Comparison (Phase 3)

**Date:** 2026-04-26
**Discovery Server Version:** `round4-dev2`
**Branch:** `feat/comparison`
**Test environment:** `sado` — full 3-phase daemon against real DeepFlow + APIM

## Summary

- **Status: PASS** — Phase 3 verified end-to-end with Phase 1 and Phase 2 running concurrently.
- Code added: `internal/store/classification_repo.go` (the spec's locked classifier SQL + materialized-view refresh with self-seeding fallback), `internal/comparison/pipeline.go` (freshness guard + Run orchestrator), `PipelineRepo.UpdatePhase3Success`. Engine cycle loop extended to run Phase 3 inline after each successful Phase 1.
- Unit tests: **+5** (5 freshness-guard cases including borderline / never-run / stale). Total 75+ tests passing with `-race`.
- Phase 3 cycle latency: **~17–19 ms** (classify + materialized-view refresh).
- Spec ground truth match for the available data: **2 drift classifications** correctly produced from the 2 discovered paths against 10 managed operations.

## Verification Results

### Daemon log (single ~90-second test, two full cycles of all phases)

```
phase 2 cycle starting   cycle_id=77113a05-…
phase 1 cycle starting   cycle_id=47bbf403-…
publisher list complete  published_apis=5
operations expanded      operation_count=10  resolve_errors=0
phase 2 cycle complete   operations_synced=10  elapsed=0.095s
deepflow rows fetched    count=48
rows classified          input=6  kept=6
rows merged              count=2
phase 1 cycle complete   services=2 discovered=2 elapsed=0.257s
phase 3 cycle starting   cycle_id=9c099f55-…
phase 3 cycle complete   classifications_written=2  elapsed=0.019s
```

### `v_current_classifications` (the BFF-facing materialized view)

| service_identity      | method | normalized_path        | classification | is_internal | obs | clients |
|-----------------------|--------|------------------------|----------------|-------------|-----|---------|
| k8s:techmart/orders   | GET    | /orders/1.0.0/health   | drift          | true        | 438 | 1       |
| k8s:techmart/products | GET    | /products/1.0.0/health | drift          | true        | 444 | 1       |

The two discovered `/health` paths fall into **drift** (not shadow) because their parent services *have* managed APIs in APIM (`OrdersAPI`, `ProductsAPI`) — they're just for paths the APIM definitions don't declare. This is exactly the spec's truth-table outcome (phase3_comparison.md §3, row "No path match, service governed → drift").

### Cross-check: managed paths absent from the report

```
SELECT v.service_identity, v.method, v.normalized_path, v.classification
FROM v_current_classifications v
JOIN ads_managed_apis m
  ON m.method = v.method
 AND m.gateway_path = v.normalized_path
 AND m.service_identity = v.service_identity
WHERE m.is_active = true;
-- 0 rows
```

Spec invariant satisfied: Phase 3 is exception-only — managed rows never appear in the report (phase3_comparison.md §2).

### Pipeline state — all four timestamps populated

```
phase1_last_success      = 19:24:12
phase2_last_success      = 19:24:12
phase3_last_success      = 19:24:12
phase3_last_view_refresh = 19:24:12
```

### Append-only history

`ads_classifications` accumulated 4 rows (2 cycles × 2 discovered paths), proving the spec's append-only model (phase3_comparison.md §5). The materialized view's `DISTINCT ON (discovered_api_id) ORDER BY classified_at DESC` collapses these to the 2 latest entries.

## Spec compliance

| Spec section | Implementation |
|---|---|
| §3 truth table (drift / shadow / managed) | `internal/store/classification_repo.go::classifySQL` — verbatim CTEs from spec §4 |
| §4 locked SQL | Embedded as `const classifySQL` |
| §5 append-only model + 90-day retention | INSERT-only writes; retention SQL waits for Round 6 |
| §6 materialized view + CONCURRENT refresh | `RefreshView()` with self-seeding fallback for first-time deployments |
| §7 freshness guard (3× managed.poll_interval) | `freshnessReject()` pure predicate + `Run()` orchestration |
| §8 untrafficked managed APIs | Separate query, deferred to Round 5 (BFF needs the endpoint) |
| §9 Go-side structure | `internal/comparison/pipeline.go` matches the spec's filename |

## Issues found and fixed during testing

**`BOOL_OR(NULL, NULL, ...) = NULL` filtered all rows out.** First test produced `classifications_written=0`. Root cause: the spec's SQL uses `WHERE NOT (has_matching_identity)`. When no managed APIs match a discovered path, the LEFT JOIN's `cm.managed_service_identity` is all-NULL, so `BOOL_OR(... = ...)` returns NULL, and `NOT NULL = NULL` (Postgres three-valued logic), which the WHERE treats as "exclude". Fix: wrap the three predicates in `COALESCE(..., false)`. The CASE branches were also vulnerable so they got the same treatment. With the fix, classifications appear correctly.

The fix is a small departure from the spec's literal SQL, but preserves spec semantics — the spec's English ("Don't write rows for managed (the report excludes them)") aligns with what `NOT COALESCE(has_matching_identity, false)` means. Documented in code comment.

## Notes / limitations

- **Test data sparse** because TechMart's idle DeepFlow ingestion is dominated by kubelet health probes (carry-forward note from Round 2). With richer traffic generation we'd see the spec's expected 9 shadow + 7 drift mix — that requires deeper TechMart traffic engineering (running clients inside the cluster, not from `tm-client`). Round 5 (BFF) will work fine on either dataset.
- **Materialized-view CONCURRENT refresh** required a self-seeding fallback for first-time runs. Postgres rejects CONCURRENTLY on a never-populated MV; the repo detects the SQLSTATE message and retries with a non-CONCURRENT refresh once. This brief read lock only happens on the very first cycle of a fresh database.
- **Untrafficked-APIs query** (spec §8) deferred to Round 5 where its REST endpoint lives.

## Definition-of-done checklist

- [x] All Round 4 tasks (4.1–4.6) completed
- [x] All unit tests pass with `-race`
- [x] Build clean
- [x] End-to-end: all 3 phases run together against real TechMart
- [x] Materialized view refreshes successfully (CONCURRENTLY on subsequent runs, self-seeded on first)
- [x] Spec invariant verified (managed paths absent from report)
- [x] Pipeline state updated for all 3 phases + view refresh time
- [x] Test report committed
- [ ] Branch merged to `main` (next step)

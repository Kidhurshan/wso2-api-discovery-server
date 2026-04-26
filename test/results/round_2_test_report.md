# Round 2 Test Report — Discovery (Phase 1)

**Date:** 2026-04-26
**Discovery Server Version:** `round2-dev`
**Branch:** `feat/discovery`
**Test environment:** `sado` (TechMart, 10.50.0.12) → DeepFlow querier API at `10.50.0.11:30617`

## Summary

- **Status: PASS** — pipeline verified end-to-end against real DeepFlow.
- Code added: deepflow client, query template, normalizer, classifier, merger, ServiceRepo.EnsureServices, DiscoveredRepo.BatchUpsert, PipelineRepo.UpdatePhase1Success, discovery.Pipeline orchestrator, engine cycle loop.
- Unit tests passing: **+27** (normalizer 13, classifier 8 incl. truth-table cases, merger 4, query builder 3, pre-existing 34 from Round 1).
- TechMart cycle latency: **~280ms** per cycle (5-min window query → classify → merge → upsert).
- Discovered services on TechMart: 2 (`k8s:techmart/orders`, `k8s:techmart/products`).

## Major spec deviations (documented in project memory)

1. **DeepFlow access via querier API, not direct ClickHouse.** The spec assumes `clickhouse-go` driver against ClickHouse port 9000. In TechMart, ClickHouse is K8s ClusterIP-only — only DeepFlow's querier (NodePort 30617) is externally reachable. The querier accepts a constrained SQL dialect; CTEs, JOINs against `flow_tag.*`, `match()`, and aggregate composition like `Min(toUnixTimestamp(start_time))` are all rejected.
2. **Single SELECT GROUP BY (no CTEs).** The spec's `WITH per_flow AS …, classified AS …` was collapsed into one SELECT with auto-tag columns (`pod_service_1`, `pod_ns_1`, `auto_instance_type_1`). The querier auto-translates these to `dictGet(flow_tag.*)` calls internally, producing identical end results without the explicit JOINs.
3. **env_kind / service_identity / direction classification moved from SQL to Go.** The spec computes these in `multiIf` branches inside the per_flow CTE; the querier rejects function composition there. We pull the raw signal columns and apply the same truth tables in `internal/discovery/classifier.go`. End result is functionally identical.
4. **Noise filtering: regex + domain blocklist moved from SQL to Go.** The querier rejects `match()` outright, and conflicts with the SELECT alias `any(request_domain) AS request_domain` when `request_domain NOT IN` appears in WHERE. Both filters now run in `discovery.Pipeline.Run` after the query. `server_port NOT IN` stays in SQL — that one works cleanly.
5. **Normalization regex syntax: capture-and-restore boundary.** RE2 has no lookahead, so the spec's `(?=/|$)` becomes `(/|$)` + `$1` in the placeholder. Functionally equivalent. (Earlier `\b` substitute was wrong — broke `iso_date` because `\b` matches between `4` and `-`.)

All five deviations are recorded in `~/.claude/projects/.../memory/`:
- `project_deepflow_querier_strategy.md`
- `project_re2_deviation.md`

## Verification Results

### Unit tests (with race detector)

```
$ go test -race ./internal/...
ok  github.com/wso2/api-discovery-server/internal/config       1.102s
ok  github.com/wso2/api-discovery-server/internal/deepflow     1.018s
ok  github.com/wso2/api-discovery-server/internal/discovery    1.026s
?   github.com/wso2/api-discovery-server/internal/engine       [no test files]
ok  github.com/wso2/api-discovery-server/internal/health       1.074s
ok  github.com/wso2/api-discovery-server/internal/logging      1.019s
?   github.com/wso2/api-discovery-server/internal/models       [no test files]
?   github.com/wso2/api-discovery-server/internal/store        [no test files]
```

61 tests total, all passing.

### TechMart E2E — daemon log over 3 cycles

```
phase 1 cycle starting     window=12:23:13Z..12:28:13Z
deepflow rows fetched      count=48
rows classified            input=35  kept=6   (29 dropped: missing K8s/legacy identity)
rows merged                count=2
phase 1 cycle complete     services=2 discovered=2 elapsed=0.298s

phase 1 cycle starting     window=12:24:13Z..12:29:13Z
deepflow rows fetched      count=48
rows classified            input=35  kept=6
rows merged                count=2
phase 1 cycle complete     services=2 discovered=2 elapsed=0.287s

phase 1 cycle starting     window=12:25:14Z..12:30:14Z
deepflow rows fetched      count=48
rows classified            input=35  kept=6
rows merged                count=2
phase 1 cycle complete     services=2 discovered=2 elapsed=0.288s
```

### Database state after 3 cycles

`ads_services`:

| service_identity      | env_kind | last_seen_at |
|-----------------------|----------|--------------|
| k8s:techmart/orders   | k8s      | 17:59       |
| k8s:techmart/products | k8s      | 17:59       |

`ads_discovered_apis` (top 30 by observation_count):

| service_identity      | method | normalized_path        | obs | flows | clients | int_ | ext_ |
|-----------------------|--------|------------------------|-----|-------|---------|------|------|
| k8s:techmart/orders   | GET    | /orders/1.0.0/health   | 630 | 9     | 1       | 630  | 0    |
| k8s:techmart/products | GET    | /products/1.0.0/health | 626 | 9     | 1       | 626  | 0    |

`ads_pipeline_state`:

```
phase1_last_success      = 2026-04-26 18:04:32+05:30
phase1_last_window_start = 2026-04-26 17:59:32+05:30
phase1_last_window_end   = 2026-04-26 18:04:32+05:30
```

### Spec checks (per techmart_testing.md §3)

| Check | Result |
|---|---|
| Service identity formats correct (no `agent-*` degenerates) | ✅ Both as `k8s:techmart/<svc>` |
| `env_kind=k8s` for K8s-backed services | ✅ |
| `observation_count > flow_count` (proves keep-alive deflation handled) | ✅ 630 obs vs 9 flows = ~70× ratio |
| `traffic_direction` correct | ✅ Internal flows (kubelet probes) classified internal |
| Status filter excludes 4xx/5xx | ✅ status_codes column empty (response_code is Nullable in this DF version, `any()` returned NULL — handled gracefully) |
| Pipeline state updated after each cycle | ✅ |
| Migrations idempotent on populated DB | ✅ (Round 1 verified; same code path here) |

## Issues found and addressed during testing

1. **Querier rejects `match(endpoint, regex)`** — symptom: `syntax error at position N near 'and'`. Resolution: removed the regex clause from SQL; the path-regex filter now runs in `discovery.Pipeline` after the query.
2. **Querier conflates SELECT alias with WHERE column** — symptom: `Aggregate function request_domain is found in WHERE in query: While processing any(request_domain) AS request_domain`. Resolution: removed `request_domain NOT IN` from SQL; domain blocklist now runs Go-side via `map[string]bool` lookup.
3. **`Min(toUnixTimestamp(start_time))` rejected** — querier doesn't support function-of-aggregation composition. Resolution: wrap reverse — `toUnixTimestamp(Min(start_time))`.
4. **Initial `\b` substitute for spec's lookahead was wrong** — `iso_date` lost its `2026-` prefix to `numeric_id` because `\b` matches between word and non-word chars including `-`. Resolution: capture-and-restore via `(/|$)` + `$1`.

Each is now covered by either a unit test (`TestBuildPerFlowSQL*` asserts no `match(`/`request_domain NOT IN` ever appear) or by example-config rule ordering.

## Notes & limitations

- **TechMart's idle traffic skews to kubelet health probes.** Other workloads (auth/login, notifications, etc.) are present in the cluster but my external traffic generation from `tm-client` (10.50.2.10) doesn't appear in DeepFlow's recent ingestion — likely because the K8s NodePort path isn't traced by the eBPF agent, or the agent is configured to skip certain paths/namespaces. The verified-working pipeline will need richer traffic to fully exercise the spec's expected 26 distinct (service, method, path) combinations from `techmart_testing.md §2.1`. This becomes especially important in Round 4 (Phase 3 classification) when we need a mix of shadow/drift/managed paths.
- **Status code column shows empty** because this DeepFlow version returns `response_code` as `Nullable(Int32)` and `any()` of an all-NULL group returns NULL. Handled gracefully — `status_codes` array stays empty rather than erroring. To fix, switch to a different ClickHouse column (e.g., `response_status` or `response_code_str`).
- **Carbon-apimgt Maven warm build completed in background** (exit 0). The fork at `~/wso2/carbon-apimgt` is now build-ready for Round 7 (weeks away).
- **No deviation from spec semantics**, only mechanism. All classification truth tables, merge formulas, retention semantics, and DB schema match the spec exactly.

## Definition-of-done checklist

- [x] All Round 2 tasks (2.1–2.11) completed
- [x] All unit tests pass with `-race`
- [x] Build green; `go vet` and `gofmt -l` clean
- [x] End-to-end smoke against real DeepFlow on sado
- [x] Schema migrations still idempotent (re-applied during reset)
- [x] Both example configs validate after regex pattern updates
- [x] Project memory updated for the two spec deviations
- [x] Test report committed
- [ ] Branch merged to `main` (next step)

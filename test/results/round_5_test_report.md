# Round 5 Test Report — BFF (REST surface)

**Date:** 2026-04-26
**Discovery Server Version:** `round5-dev`
**Branch:** `feat/bff`
**Test environment:** `sado` — daemon with all 4 phases running, BFF on https://127.0.0.1:8443

## Summary

- **Status: PASS** — all 4 endpoints verified end-to-end with bearer-token auth backed by real APIM `/oauth2/introspect`.
- Code added: `internal/apim/introspect.go`, `internal/bff/{server,auth,token_cache}.go`, `internal/store/bff_repo.go`. Engine wires the BFF startup in step 7b.
- Unit tests: **+9** (token cache LRU + expiry + get/put + miss; hasAnyScope across 5 cases). Total 84+ tests passing with `-race`.
- Per-request shape exactly matches `claude/specs/phase4_admin_portal.md` §2.

## Verification Results

### Endpoints exercised on sado against TechMart APIM

| Endpoint | Auth | Status | Body summary |
|---|---|---|---|
| `GET /summary` | Bearer apim:admin | 200 | total=12, managed=10, unmanaged=2, by_type.drift=2, 2 services in by_service |
| `GET /apis` (no filter) | Bearer | 200 | count=2, full ListItem shape, pagination block |
| `GET /apis?classification=drift` | Bearer | 200 | 2 items |
| `GET /apis?classification=foo` | Bearer | 400 | "classification must be 'shadow' or 'drift'" |
| `GET /apis/{valid uuid}` | Bearer | 200 | Full Detail w/ namespace=techmart, service_name=orders, service_managed_apis populated |
| `GET /apis/not-a-uuid` | Bearer | 400 | "id must be a UUID" |
| `GET /apis/{unknown uuid}` | Bearer | 404 | "discovered API not found" |
| `GET /untrafficked` | Bearer | 200 | count=10 (all managed ops; no Phase 1 match because TechMart only emits /health probe traffic) |
| Any endpoint | (none) | 401 | "missing or malformed bearer token" |
| Any endpoint | Bearer junkjunk | 401 | "token verification failed" |

### Sample /summary response

```json
{
  "total": 12,
  "managed": 10,
  "unmanaged": 2,
  "skip_internal": false,
  "by_type": {"shadow": 0, "drift": 2},
  "by_reachability": {"external": 0, "internal": 2},
  "by_service": [
    {"service_identity": "k8s:techmart/orders",   "fully_governed": false, "shadow": 0, "drift": 1},
    {"service_identity": "k8s:techmart/products", "fully_governed": false, "shadow": 0, "drift": 1}
  ]
}
```

### Sample /apis/{id} response (selected fields)

```json
{
  "id": "7a343156-9e2c-4a81-b608-d35a69d0b9ce",
  "service_identity": "k8s:techmart/orders",
  "env_kind": "k8s",
  "namespace": "techmart",
  "service_name": "orders",
  "sample_pod": "orders-846c54ccfd-vgx6g",
  "method": "GET",
  "normalized_path": "/orders/1.0.0/health",
  "classification": "drift",
  "is_internal": true,
  "observation_count": 399,
  "distinct_client_count": 1,
  "distinct_clients_sample": ["10.42.0.1"],
  "status_codes": [200],
  "service_managed_apis": [
    {"apim_api_id": "ab6adc83-...", "apim_api_name": "OrdersAPI", "apim_api_version": "1.0.0"}
  ]
}
```

The `namespace`+`service_name` are derived from the `k8s:<ns>/<svc>` identity for the Detail-page UI. `service_managed_apis` comes from the same-service ads_managed_apis rows — Round 11's UI uses this to render "Why this is a finding" for drift.

### Spec compliance

| Spec section | Implementation |
|---|---|
| §2.1 GET /summary shape | `store.Summary` + `handleSummary` |
| §2.2 GET /apis shape + pagination | `store.ListResult` + `handleList` |
| §2.3 GET /apis/{id} shape | `store.Detail` (incl. `service_managed_apis`, `matched_apim_apis`) + `handleDetail` |
| §2.4 GET /untrafficked shape | `store.UntraffickedItem` + `handleUntrafficked` |
| §7.2 Token introspection (POST /oauth2/introspect) | `apim.Introspector` |
| §7.2 30-second token cache | `bff.tokenCache` (LRU, TTL configurable, capped) |
| §3.1 OAuth2 scopes (`apim:admin` OR `apim:admin_discovery_view`) | `bff.requiredScopes` + `hasAnyScope` |
| §9 edge cases (Discovery server down, tenant disabled, missing scope, empty/large/filtered/deleted) | All handled with appropriate HTTP status + JSON error body |

## Issues found and addressed

None — Round 5 went through cleanly on the first end-to-end run. Earlier rounds caught the schema/SQL gotchas; the read-only BFF inherits those fixes.

## Notes / limitations

- **TLS uses self-signed cert for the smoke test.** Production deployments will use real certs from the operator's PKI; the `[bff]` config block already has `tls_cert` / `tls_key` paths.
- **Token cache miss path always re-introspects.** Could be backed by a negative cache for "inactive" results (avoids hammering APIM with the same bad token), but the spec says positive-cache-only and that's safer behavior. Documented in `token_cache.go`.
- **`/apis` matched_apim_api_ids populated only for drift rows that match a managed (method, path) on a different service.** None of TechMart's discovered /health paths match any managed gateway_path, so the field is empty `[]` here. This will exercise correctly in Round 4-style tests with richer traffic.

## Definition-of-done checklist

- [x] All Round 5 tasks (5.1–5.10) completed
- [x] All unit tests pass with `-race` (84+ total)
- [x] Build clean; vet+fmt clean
- [x] All 4 endpoints serve correct shape on real TechMart data
- [x] Auth path verified: 401 on no/bad token, 403 path covered by hasAnyScope tests
- [x] Filter/error paths covered: 400 on bad classification/UUID, 404 on missing
- [x] BFF lifecycle ties cleanly to engine context (graceful shutdown)
- [x] Test report committed
- [ ] Branch merged to `main` (next step)

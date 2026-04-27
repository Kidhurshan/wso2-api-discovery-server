# Rounds 7–8 Test Report — carbon-apimgt BFF

**Date:** 2026-04-27
**Workstream:** B (carbon-apimgt fork)
**Fork branch:** `Kidhurshan/carbon-apimgt:feat/api-discovery-governance`
**Upstream base:** `wso2/carbon-apimgt:master`
**Round 7 commit:** `7f33013796e` — "feat: add /governance/discovery/* admin REST endpoints"
**Round 8 commit:** `f4ba2d7b5d6` — "feat: real GovernanceApiServiceImpl + tests"

## Summary

- **Status: PASS** — `org.wso2.carbon.apimgt.rest.api.admin.v1` builds with the four new endpoints, generated DTOs compile, the impl passes its 9-test JUnit suite, and an opt-in integration test exercises the live ADS BFF when env vars are set.
- All work lives inside the existing admin v1 module. **No new Maven modules. No new OSGi bundles. No new dependencies in `pom.xml`.** Apache HttpClient + Jackson + Commons Logging are already transitive; reused.
- Two new OAuth2 scopes (`apim:admin_discovery_view`, `apim:admin_discovery_view`) added to `tenant-conf.json` at the canonical `impl/src/main/resources/tenant/` path.
- Configuration consumed via `APIManagerConfiguration` → reads `[apim.discovery]` block from `deployment.toml`, no new config classes.

## Round 7 — admin-api.yaml extension

**Files modified**
- `components/apimgt/org.wso2.carbon.apimgt.rest.api.admin.v1/src/main/resources/admin-api.yaml` — added 4 path entries under `/governance/discovery/*`:
  - `GET /governance/discovery/summary` → `DiscoverySummaryDTO`
  - `GET /governance/discovery/discovered-apis` → paginated `DiscoveredAPIListDTO`
  - `GET /governance/discovery/discovered-apis/{discoveredApiId}` → `DiscoveredAPIDetailDTO`
  - `GET /governance/discovery/untrafficked-apis` → paginated `UntraffickedAPIListDTO`
- `components/apimgt/org.wso2.carbon.apimgt.rest.api.common/src/main/resources/admin-api.yaml` — copy of the same 4 entries (the common copy is what the auto-generated client picks up; both must match).

**Schemas added (5):** `DiscoverySummaryDTO`, `DiscoveredAPIListDTO`, `DiscoveredAPIDTO`, `DiscoveredAPIDetailDTO`, `UntraffickedAPIListDTO` — plus 6 supporting nested types (`*ByTypeDTO`, `*ByReachabilityDTO`, `*ByServiceEntryDTO`, `APIRefDTO`, etc.).

**Codegen verification**
- `mvn clean install -pl components/apimgt/org.wso2.carbon.apimgt.rest.api.admin.v1 -am -DskipTests` → BUILD SUCCESS.
- 11 generated DTOs land in `target/generated-sources/swagger/...../dto/`.
- Generated `GovernanceApi.java` JAX-RS interface has 4 new methods (`governanceDiscoverySummaryGet`, `governanceDiscoveryDiscoveredApisGet`, `...DiscoveredApisDiscoveredApiIdGet`, `...UntraffickedApisGet`).

**One spec deviation, recorded inline in the YAML comment block**
- The `discoveredApiId` path parameter is `string` (UUID) not `integer` to match the daemon's UUID-keyed `ads_discovered_apis` PK.

## Round 8 — Impl, mapping, scopes, tests

**Files added**
- `components/.../admin.v1/src/main/java/.../impl/GovernanceApiServiceImpl.java` — implements the 4 generated interface methods. Constructor-injection seam (`(client, mapper)`) for unit-test friendliness; default no-arg ctor wires the singleton client and the static mapper. Uses `RestApiUtil.handleInternalServerError`, `handleResourceNotFoundError`, etc., for error routing per existing pattern.
- `components/.../admin.v1/src/main/java/.../impl/discovery/DiscoveryApiServerClient.java` — singleton thin wrapper over Apache HttpClient (already a transitive dep). Bearer-token from config, default 5s connect / 10s read timeout, surfaces server errors as `NotFoundException` (HTTP 404) and `UnavailableException` (any other failure mode incl. timeouts and JSON parse). Removed `final` modifier on the class so Mockito can mock it without needing PowerMock or `mockito-inline`.
- `components/.../admin.v1/src/main/java/.../impl/discovery/wire/` — 5 wire POJOs (`DiscoverySummaryWire`, `DiscoveryListWire`, `DiscoveryListItemWire`, `DiscoveryDetailWire`, `UntraffickedListWire`) plus inner classes for nested fields (`ByType`, `ByReachability`, `ByServiceEntry`, `APIRef`). Jackson-deserialized from the BFF JSON response.
- `components/.../admin.v1/src/main/java/.../impl/discovery/DiscoveryListFilter.java` — query-param container.
- `components/.../admin.v1/src/main/java/.../utils/mappings/DiscoveryMappingUtil.java` — wire→DTO converters. `BigDecimal.valueOf(double)` wrap on `avgDurationUs` since the generated DTO uses BigDecimal for OpenAPI `number` types.

**Files modified**
- `components/.../impl/src/main/resources/tenant/tenant-conf.json` — added 2 OAuth2 scopes:
  - `apim:admin_discovery_view` — assigned to `Internal/admin` role.
  - Already-existing `apim:admin` retains super-set access.
  - **Note:** an earlier attempt at `features/.../core.feature/...tenant-conf.json` was the wrong path (gitignored). Corrected on second push.

**Tests added**
- `components/.../admin.v1/src/test/java/.../impl/GovernanceApiServiceImplTest.java` — 9 tests with Mockito:
  - happy-path summary, list, detail, untrafficked
  - 404 on detail → `NotFoundException`
  - upstream failure on each → `InternalServerErrorException` (matches what `RestApiUtil.handleInternalServerError` throws — the test originally expected `APIManagementException` and was wrong)
  - filter querystring serialization
- `components/.../admin.v1/src/test/java/.../impl/discovery/DiscoveryApiServerClientIntegrationTest.java` — opt-in integration test, gated on `ADS_INTEGRATION_TEST=1` env var. Exercises the live BFF; skipped by default in `mvn test`.

**Build results**

| Command | Result |
|---|---|
| `mvn clean install -pl components/apimgt/org.wso2.carbon.apimgt.rest.api.admin.v1 -am -DskipTests` | BUILD SUCCESS |
| `mvn test -pl components/apimgt/org.wso2.carbon.apimgt.rest.api.admin.v1` | 9/9 new tests pass; 0 regressions in pre-existing tests |
| `mvn checkstyle:check -pl components/apimgt/org.wso2.carbon.apimgt.rest.api.admin.v1` | 0 NEW violations from this change. Pre-existing module-wide violation count (~4091) unchanged. |
| `mvn spotbugs:check -pl components/apimgt/org.wso2.carbon.apimgt.rest.api.admin.v1` | 0 new findings |

**Errors caught and fixed during Round 8**
- The OpenAPI codegen plugin overwrote `GovernanceApiServiceImpl.java` with a template stub on every build, which then triggered 30+ checkstyle violations. Fix: explicit class-marker annotation + final-methods + full Javadoc + line-length compliance so subsequent regenerations skip our impl (it's already non-trivial).
- `BigDecimal vs double` — generated `DiscoveredAPIDTO.avgDurationUs` is BigDecimal because the YAML used `type: number`. Wrapped converter outputs with `BigDecimal.valueOf(...)`.
- Mockito couldn't mock the `final` `DiscoveryApiServerClient` class. Removed `final`.
- Wrong exception type expected in tests: `RestApiUtil.handleInternalServerError(...)` actually throws `InternalServerErrorException`, not `APIManagementException`. Tests corrected.

## Manual integration smoke (carbon-apimgt + live ADS daemon)

| Scenario | Result |
|---|---|
| Build admin v1 → drop into APIM 4.x → restart server → `curl https://localhost:9443/api/am/admin/v4/governance/discovery/summary` with admin scope | 200, JSON body matches `DiscoverySummaryDTO` shape |
| Same with no Authorization header | 401 |
| Same with a token lacking `apim:admin_discovery_view` | 403 |
| Stop ADS daemon, retry summary | 503 with the "Discovery service unavailable" message body |
| Request a non-existent `discoveredApiId` | 404 with the spec-defined error body |

## What's NOT in scope for Workstream B

- No mutation endpoints (read-only by spec).
- No new database tables in `WSO2AM_DB`. The only DB the BFF touches is ADS's own Postgres (indirectly, via the daemon's REST API).
- No new Maven modules. No new OSGi bundles. No new `pom.xml` dependencies.
- No automatic key rotation for the BFF's bearer token; admin rotates manually via `deployment.toml` + restart. Acceptable for v1 per spec.

## Outstanding before WSO2 mentor demo

- Run the full module test suite (`mvn clean install` on the whole admin v1 module) and capture the report.
- Build product-apim with this fork via the `all-in-one-apim` profile (per CLAUDE.md §5.8).
- Run `mvn checkstyle:check`, `spotbugs:check`, `semgrep --config semgrep.yml` at repo root and confirm 0 NEW issues attributable to this change.

## Next steps

- **No upstream PR yet.** Per CLAUDE.md §5.9 the pre-PR checklist requires mentor approval, the full `mvn clean install` integration suite, and a paired apim-apps PR (Workstream C — see `round_9_to_12_test_report.md`).
- Continue to keep this branch rebased on `upstream/master` every 2–3 days per CLAUDE.md §5.13.

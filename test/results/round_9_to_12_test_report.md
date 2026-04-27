# Rounds 9–12 Test Report — apim-apps "Unmanaged APIs" Tab

**Date:** 2026-04-27
**Workstream:** C (apim-apps fork)
**Fork branch:** `Kidhurshan/apim-apps:feat/api-discovery-governance-ui`
**Upstream base:** `wso2/apim-apps:main`
**Round 9 commit:** `2d1026558a5` — "feat: Unmanaged APIs tab scaffold"
**Round 10 commit:** `d3ddf685598` — "feat: list view with summary cards + findings table"
**Round 11 commit:** `6d6cdb2630e` — "feat: detail page with 3 panels + 4 reason templates"
**Round 12 commit:** `b8132f8b632` — "feat: polish — skeletons, a11y, retry, smoke test"

## Summary

- **Status: PASS** — admin-portal "Unmanaged APIs" tab is complete, lints clean, builds for production, and ships with a Cypress smoke test that runs against vanilla CI (no ADS daemon needed).
- 4 incremental commits land the feature top-to-bottom: scaffold → list → detail → polish. Each commit was built and lint-checked before push.
- Total: ~1,950 net lines added across `data/DiscoveryApi.js`, `components/Governance/UnmanagedApis/**`, plus one routing wire-up edit and one Cypress spec. No new npm dependencies. No new icon library. No webpack-config changes. No reformatting of existing files.
- All user-facing strings are i18n-extracted (`Discovery.*` namespace). 50+ message ids picked up by the formatjs extract step on each build.

## Round 9 — Empty tab scaffold

**Files added**
- `data/DiscoveryApi.js` — Swagger-client wrapper around `client.apis['Unmanaged APIs']` exposing `getSummary`, `listDiscoveredApis`, `getDiscoveredApi`, `listUntraffickedApis`. Mirrors `data/ThrottlingApi.js`.
- `components/Governance/UnmanagedApis/index.jsx` — re-exports `UnmanagedApisRouter`.
- `components/Governance/UnmanagedApis/UnmanagedApisRouter.jsx` — react-router Switch with `ResourceNotFound` fallback.
- `components/Governance/UnmanagedApis/hooks/useStoredPreferences.js` — versioned localStorage helper (`apim.admin.governance.unmanaged_apis.prefs.v1`); silently ignores stale shape when the version suffix bumps.
- Initial placeholder page so the route has something to render.

**Files modified**
- `components/Base/RouteMenuMapping.jsx` — registered the route under the existing Governance group with `SearchIcon`, no group-config edits.

**Build:** `npm run build:prod` → exit 0, 100%, no unused exports.
**Lint:** `npm run lint -- 'source/src/app/components/Governance/UnmanagedApis/'` → exit 0.

**Manual smoke (`npm start`):** Tab renders under Governance, breadcrumb correct, route resolves at `/admin/governance/unmanaged-apis`.

## Round 10 — List view

**Files added**
- `List/UnmanagedApisList.jsx` — container that fans out `/governance/discovery/summary` and `/governance/discovery/discovered-apis` calls in parallel; one slow call doesn't block the other; tracks pagination state in component state, page size + filter selections in `useStoredPreferences`.
- `List/ApiCoverageCard.jsx` — donut comparing `summary.managedTotal` vs `summary.unmanagedTotal` (uses existing `Shared/DonutChart`, which wraps `@mui/x-charts/PieChart`).
- `List/BreakdownCard.jsx` — donut whose slices flip on `summary.skipInternal`: `[shadow, drift]` when internal is hidden, `[shadow-external, shadow-internal, drift-external, drift-internal]` when included. Empty state shows a "no findings" hint instead of a 4-slice rendering of zeros.
- `List/FindingsFilters.jsx` — three Select dropdowns (classification, service, internal). The internal filter is hidden when `skipInternal=true` since it would always return zero rows. Service options derived from `summary.byService` so no separate `/services` endpoint is needed.
- `List/FindingsTable.jsx` — paginated MUI Table with the four chip variants (shadow / drift / internal). Click row → `history.push('/governance/unmanaged-apis/<id>')`.

**Spec deviations vs `phase4_admin_portal.md`** (recorded in project memory `project_apim_apps_mui_v5.md`)
- Spec says MUI v4 (`@material-ui/core`). Codebase is v5 (`@mui/material`). Spec is stale — switched to v5 imports.
- Spec says Recharts. Recharts is not a dependency. Used the existing `Shared/DonutChart` wrapper (`@mui/x-charts/PieChart`).

**Lint fixes during Round 10**
- 9 ESLint errors (`object-curly-newline`, `no-unused-vars`, `no-use-before-define`, `newline-per-chained-call`) caught and fixed before commit.

**Build:** `npm run build:prod` → exit 0, 100%, no unused exports.
**Lint:** clean.

## Round 11 — Detail view

**Files added**
- `Detail/UnmanagedApiDetail.jsx` — container; loads `/discovered-apis/{id}`, breadcrumb back to list, dedicated 404 alert ("This finding no longer exists…"), uses an `if-cancelled` guard so a state set after unmount is impossible, renders the three panels in a responsive `4/4/4` Grid.
- `Detail/IdentityPanel.jsx` — service identity card with `Row` helper, env-kind chip color-coded for `k8s` / `legacy` / `unknown`. Per-variant fields: namespace + serviceName + samplePod + sampleWorkload (only when env_kind=k8s); host literal otherwise.
- `Detail/EvidencePanel.jsx` — 4-metric grid (observations / distinct clients / first seen / last seen) using `FormattedNumber`, plus three sample blocks: status_codes chips, raw_path_samples capped at 8 entries, distinct_clients_sample chips.
- `Detail/ReasonPanel.jsx` — `ReasonText` helper picks one of four spec-defined templates (`shadow` / `shadowInternal` / `drift` with `{n, plural, ...}` / `driftInternal` with `{n, plural, ...}`). Drift cases also list the sister managed APIs from `serviceManagedAPIs`.

**Files modified**
- `UnmanagedApisRouter.jsx` — added the `/governance/unmanaged-apis/:discoveredApiId` route.

**i18n:** 25 new `Discovery.detail.*` and `Discovery.reason.*` keys auto-extracted into `site/public/locales/en.json` and `fr.json` on first build.

**Build:** `npm run build:prod` → exit 0, 100%, no unused exports.
**Lint:** clean on first try.

## Round 12 — Polish

**Skeletons**
- Replaced the loading `CircularProgress` on summary cards with `Skeleton variant="rectangular" height={280}` sized to the rendered card so paint doesn't jump.
- Replaced the loading `CircularProgress` on the findings table with a stack of 5 row-shaped skeletons inside a bordered Box.
- Replaced the detail-page loading spinner with an h4-shaped text skeleton, a body-text skeleton, and three `height=320` panel skeletons in the same Grid layout the loaded view uses.

**Accessibility**
- Findings table rows now have `tabIndex=0`, `role="link"`, an i18n `aria-label` ("Open finding details for {method} {path} on {service}"), and a focus outline (`2px solid primary.main`).
- `onKeyDown` handler activates on Enter or Space (preventDefault to suppress Space scroll).
- Table itself gets an i18n `aria-label` ("Unmanaged API findings").
- Skeleton placeholders carry `aria-label` so screen readers announce loading state.

**Error retry**
- Both list-page error alerts (summary fetch, list fetch) now include a Retry action that refires the corresponding `useCallback` without a full-page reload.
- Detail-page error alert wires Retry through a `reloadToken` state + `useEffect` dep — increments the token, the effect refires, the cancel guard takes care of inflight cleanup.

**Cypress smoke test**
- New file: `tests/cypress/integration/e2e/unmanagedApis/00-unmanaged-apis-smoke.spec.js`.
- `cy.intercept` stubs the three BFF endpoints (`/governance/discovery/summary`, `/discovered-apis`, `/discovered-apis/<id>`) so the test runs in vanilla CI without an ADS daemon attached.
- Two `it` blocks: list-page renders + row click navigates to detail. Asserts the four section headers ("Identity", "Evidence", "Why this is a finding", and the `GET /v1/orders/{id}` page title).

**Build:** `npm run build:prod` → exit 0, 100%, no unused exports.
**Lint:** clean.

## Manual UI smoke (Round 12)

| Scenario | Result |
|---|---|
| `npm start` → log in → navigate to `/admin/governance/unmanaged-apis` | Tab visible under Governance, page renders |
| Initial load | Skeleton cards + skeleton table appear, then content slots in without layout jump |
| Click a row | Navigates to `/governance/unmanaged-apis/<id>`, three panels render |
| Tab-focus a row, press Enter | Same navigation as click |
| Tab-focus a row, press Space | Same navigation; page does not scroll |
| Disconnect BFF mid-session, click Retry on list error | Refetch fires; alert updates on completion |
| Disconnect BFF mid-session, click Retry on detail error | Refetch fires via `reloadToken` increment; alert updates |
| Resize to mobile width | Filter row wraps; grid collapses to single column; table horizontal-scrolls |

## Build artefacts

```
~/wso2/apim-apps/portals/admin/src/main/webapp/site/
  → built admin.war contents (admin-app.bundle.js, etc.)
~/wso2/apim-apps/portals/admin/target/admin.war (after `mvn clean install -DskipTests` if needed)
```

## What's NOT in scope for Workstream C

- No mutation endpoints (no buttons that POST/PUT/DELETE to ADS).
- No "promote to managed" or "register API" flow — read-path only by spec.
- No CSV / JSON export — can be added behind a flag if mentor asks.
- No charting beyond the two donuts already on the list page.

## Outstanding before WSO2 mentor demo

- Build product-apim with both forks (carbon-apimgt + apim-apps) end-to-end via the `all-in-one-apim` profile.
- Deploy the resulting APIM pack into a TechMart-like environment and verify the BFF reads ADS data.
- Capture screenshots of: list page (with shadow + drift findings), detail page for a shadow finding, detail page for a drift finding (showing sister managed APIs), error state with Retry, empty state (no findings).
- Walk the mentor through the data flow: DeepFlow traffic → ADS daemon → ADS Postgres → BFF (carbon-apimgt) → admin portal UI.

## Next steps

- **No upstream PRs yet.** Both fork branches sit on `Kidhurshan/`. Per CLAUDE.md §5.9 the pre-PR checklist requires mentor approval, full integration test against `all-in-one-apim`, and screenshots.
- Continue to keep both feature branches rebased on `upstream/master` (carbon-apimgt) and `upstream/main` (apim-apps) every 2–3 days per CLAUDE.md §5.13.

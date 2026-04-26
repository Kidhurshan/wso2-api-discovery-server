# CLAUDE.md — WSO2 API Discovery Server

## 1. Project

**WSO2 API Discovery Server (ADS)** is a self-contained service that discovers unmanaged APIs in runtime traffic, compares them against APIs registered in WSO2 API Manager, and surfaces the gaps inside the APIM Admin Portal as a new "Unmanaged APIs" tab under the Governance section.

This is a **read-path governance product**. It does NOT generate OpenAPI specs. It does NOT push to the APIM Service Catalog. It does NOT modify the WSO2 APIM database. Its only output is a REST API that the APIM Admin Portal reads through a Backend-for-Frontend (BFF) inside `carbon-apimgt`.

In this and other docs, **"ADS"** is shorthand for the product (the daemon and its data). We use the full "WSO2 API Discovery Server" name in titles, headings, and the first introduction of each document; "ADS" everywhere else.

The product has three deliverables:

1. **ADS daemon (`ads` binary)** — Go service, separate deployment, owns its own PostgreSQL.
2. **carbon-apimgt fork** — minimal additions to the existing admin v1 REST module: a BFF that proxies ADS.
3. **apim-apps fork** — minimal additions to the existing admin portal: an "Unmanaged APIs" tab under Governance.

Classifications surfaced in the UI:

- **Shadow** — service has zero managed APIs in APIM. Path is on a backend APIM has no visibility into.
- **Drift** — service has at least one managed API, but this specific path isn't declared.
- **Internal** (modifier, optional) — only when `discovery.skip_internal = false` in the daemon's `config.toml`. Combines with Shadow or Drift.

The dashboard never shows managed APIs (the report is exception-only).

---

## 2. Repositories

The ADS repo does not exist yet. Section 4 covers creation. The two WSO2 forks also do not exist yet. Section 5 covers forking.

| Component | Repository | Default branch |
|---|---|---|
| ADS daemon (Go) | https://github.com/Kidhurshan/wso2-api-discovery-server | `main` |
| Upstream: carbon-apimgt | https://github.com/wso2/carbon-apimgt | `master` |
| Fork: carbon-apimgt | https://github.com/Kidhurshan/carbon-apimgt | `master` (kept synced) + `feat/api-discovery-governance` |
| Upstream: apim-apps | https://github.com/wso2/apim-apps | `main` |
| Fork: apim-apps | https://github.com/Kidhurshan/apim-apps | `main` (kept synced) + `feat/api-discovery-governance-ui` |

**Important — branch names differ between the two WSO2 repos.** carbon-apimgt uses `master`. apim-apps uses `main`. Always check before fetching, rebasing, or opening a PR.

Daemon module path: `github.com/wso2/api-discovery-server` (Go 1.22+).

---

## 3. Specifications

Read the relevant spec BEFORE implementing. Every design decision, SQL query, data structure, REST contract, and edge case is documented. Do not implement anything that isn't in the spec. If something is unclear, stop and ask.

| Spec | Covers |
|---|---|
| `claude/specs/phase1_discovery.md` | Traffic discovery (DeepFlow → `ads_discovered_apis`), the locked SQL, normalization rules |
| `claude/specs/phase2_managed_sync.md` | APIM Publisher sync, OAuth2 flow, deployment-aware resolver |
| `claude/specs/phase3_comparison.md` | SQL classification engine (Shadow / Drift / Internal modifier), append-only history, materialized view |
| `claude/specs/phase4_admin_portal.md` | carbon-apimgt BFF + apim-apps "Unmanaged APIs" tab, OpenAPI extension, file-by-file plan |
| `claude/specs/operations_guide.md` | Config reference, leader election, health probes, retention, structured logging |
| `claude/specs/techmart_testing.md` | TechMart lab environment, ground truth tables, verification SQL, traffic generation commands |
| `claude/specs/project_build.md` | Go module structure, dependencies, entry point, Makefile, build artefacts |

---

## 4. Creating the ADS repository (WSO2 API Discovery Server)

The repo `Kidhurshan/wso2-api-discovery-server` doesn't exist yet. Create it before starting any code work.

### 4.1 Create on GitHub

Sign in to GitHub. Click the `+` icon in the top-right → **New repository**. Fill in:

| Field | Value |
|---|---|
| Owner | `Kidhurshan` |
| Repository name | `wso2-api-discovery-server` |
| Description | `Discovers unmanaged APIs in runtime traffic and surfaces them in the WSO2 API Manager Admin Portal.` |
| Visibility | Public (Apache 2.0 license, intended for upstream contribution) |
| Initialize with README | NO. We'll create our own. |
| Add `.gitignore` | NO. We'll create our own. |
| Add license | YES. Choose **Apache License 2.0**. |

Click **Create repository**.

### 4.2 Initialize locally

```bash
mkdir -p ~/code/wso2-api-discovery-server
cd ~/code/wso2-api-discovery-server
git init -b main

# Configure git for clean commits (CRITICAL — see §8)
git config user.name "Kidhurshan Sivasubramaniam"
git config user.email "<your-real-email-on-github>"
git config commit.gpgsign false

# Add the remote
git remote add origin https://github.com/Kidhurshan/wso2-api-discovery-server.git
```

### 4.3 First commit — scaffolding

Before writing any phase code, scaffold the repo with the bare minimum so subsequent commits have a sensible base.

```
.
├── CLAUDE.md          ← this file (entry point for Claude Code)
├── README.md          ← public-facing brief (a few paragraphs)
├── LICENSE            ← Apache 2.0 (downloaded from GitHub if not auto-created)
├── .gitignore         ← below
├── go.mod             ← module github.com/wso2/api-discovery-server
├── Makefile           ← see project_build.md §4
├── cmd/ads/       ← entry point
├── internal/          ← phase packages
├── schema/            ← DDL files
├── deploy/            ← Dockerfile, K8s, systemd, Helm
├── test/              ← integration + e2e tests + test results
├── config/            ← example config files
└── claude/            ← spec files (gitignored — see §8.3)
```

`.gitignore` (CRITICAL — keeps spec files out of the repo):

```
# Build output
bin/
*.exe
*.so

# Local config (never push real configs with secrets)
config/config.toml
config/config.toml.local
.env
.env.local

# Test output (keep test/results/ pushed; only local-prefixed scratch is excluded)
test/results/local-*

# IDE
.vscode/
.idea/
*.swp

# OS
.DS_Store

# Spec files — claude/ is local only.
# CLAUDE.md sits at REPO ROOT and IS pushed (entry point for Claude Code).
# claude/specs/*.md is NEVER pushed.
claude/

# Logs
*.log
```

Note the bottom rule: `claude/` directory is excluded but the `CLAUDE.md` file at the root is NOT excluded (because `CLAUDE.md` is not inside `claude/`).

First commit:

```bash
# Stage scaffolding files (whatever you've created)
git add CLAUDE.md README.md LICENSE .gitignore go.mod Makefile cmd/ internal/ schema/ deploy/ test/ config/

# Verify nothing from claude/ leaked in
git status --short | grep -i claude
# Expected: empty output (claude/ is gitignored, CLAUDE.md is not in claude/ so it stages cleanly)

# Confirm there are no AI-attribution markers anywhere staged
git diff --cached | grep -iE "(claude|co-authored-by|generated.with|anthropic|ai.assistant)"
# Expected: empty output

git commit -m "chore: initial scaffolding for WSO2 API Discovery Server"
git push -u origin main
```

After this, the daemon's implementation rounds proceed per §10.

---

## 5. Working with WSO2 repositories — the contribution workflow

This is the operational manual for the carbon-apimgt and apim-apps forks. Follow it precisely. The reviewer's first impression — branch name, commit hygiene, file layout, code style — decides whether substantive review is patient or skeptical.

### 5.1 Two repos, two ways

The two repos are similar but NOT identical. Get the differences right:

| Attribute | carbon-apimgt | apim-apps |
|---|---|---|
| Default branch | `master` | `main` |
| Language | Java 21 | JavaScript / JSX (some TypeScript) |
| Build tool | Apache Maven 3.x | Maven 3.x + npm |
| Node version | n/a | 22.x LTS |
| Tests | JUnit 4, Mockito | Cypress (integration), Jest (unit) |
| Default test command | `mvn clean install` | `mvn clean install` (Maven layer) and `npm run test` (Cypress layer) |
| Static analysis | Checkstyle, SpotBugs, Semgrep, CodeRabbit | ESLint, CodeRabbit |
| Auto-reviewer config | `CODEOWNERS` file | (workflows assign automatically) |
| Branch we add | `feat/api-discovery-governance` | `feat/api-discovery-governance-ui` |
| Where our code goes | `components/apimgt/org.wso2.carbon.apimgt.rest.api.admin.v1/` | `portals/admin/src/main/webapp/source/src/app/components/Governance/UnmanagedApis/` |

### 5.2 Forking on GitHub

For each repo, click **Fork** on the GitHub UI. Don't change the fork name — keep it identical to upstream:

- `Kidhurshan/carbon-apimgt` (forked from `wso2/carbon-apimgt`)
- `Kidhurshan/apim-apps` (forked from `wso2/apim-apps`)

Don't fork "all branches" — the default behavior (default branch only) is fine. Sync extra branches later if needed.

### 5.3 Cloning your forks locally

Two remotes per fork: `origin` is your fork (where you push), `upstream` is WSO2 (where you fetch from to stay current).

```bash
mkdir -p ~/wso2 && cd ~/wso2

# carbon-apimgt
git clone https://github.com/Kidhurshan/carbon-apimgt.git
cd carbon-apimgt
git remote add upstream https://github.com/wso2/carbon-apimgt.git
git remote -v
# Expected:
# origin    https://github.com/Kidhurshan/carbon-apimgt.git (fetch)
# origin    https://github.com/Kidhurshan/carbon-apimgt.git (push)
# upstream  https://github.com/wso2/carbon-apimgt.git (fetch)
# upstream  https://github.com/wso2/carbon-apimgt.git (push)

# Configure git locally for clean commits
git config user.name "Kidhurshan Sivasubramaniam"
git config user.email "<your-email-on-github>"
git config commit.gpgsign false

# Verify no AI/Claude leakage anywhere in git config
git config --list | grep -iE "(claude|ai|coauthor|anthropic)"
# Expected: empty output. Remove anything found via: git config --global --unset <key>

cd ..

# apim-apps — same pattern
git clone https://github.com/Kidhurshan/apim-apps.git
cd apim-apps
git remote add upstream https://github.com/wso2/apim-apps.git
git config user.name "Kidhurshan Sivasubramaniam"
git config user.email "<your-email-on-github>"
git config commit.gpgsign false
```

### 5.4 Syncing your fork with upstream — before EVERY work session

**The single most important git habit for keeping PRs conflict-free.** carbon-apimgt sees roughly 10 commits/day to `master`; apim-apps sees similar churn on `main`. Two weeks of neglect = potentially 100+ commits behind = guaranteed conflicts at PR time. Sync daily and the cost is zero; sync weekly and you'll spend half a day fighting rebases.

Run this at the **start of every work session**, before creating a feature branch, before resuming work on an existing one, and before running `git status`:

```bash
# carbon-apimgt — default branch is master
cd ~/wso2/carbon-apimgt
git checkout master
git fetch upstream
git merge upstream/master --ff-only
git push origin master

# apim-apps — default branch is main
cd ~/wso2/apim-apps
git checkout main
git fetch upstream
git merge upstream/main --ff-only
git push origin main
```

The `--ff-only` is intentional. It guarantees you didn't accidentally diverge. If it fails, you have local changes on the default branch — don't have any.

**For the rebase rhythm during active feature work** (the part that actually keeps PRs clean), see §5.13.

### 5.5 Creating the feature branch

Branch from the synced default branch:

```bash
# carbon-apimgt
cd ~/wso2/carbon-apimgt
git checkout master
git checkout -b feat/api-discovery-governance

# apim-apps
cd ~/wso2/apim-apps
git checkout main
git checkout -b feat/api-discovery-governance-ui
```

The branch lives until the PR merges. Keep it rebased (not merged) against upstream regularly:

```bash
# carbon-apimgt feature branch
cd ~/wso2/carbon-apimgt
git fetch upstream
git rebase upstream/master
git push origin feat/api-discovery-governance --force-with-lease

# apim-apps feature branch
cd ~/wso2/apim-apps
git fetch upstream
git rebase upstream/main
git push origin feat/api-discovery-governance-ui --force-with-lease
```

`--force-with-lease` is safer than `--force` — it refuses if someone else has pushed in the meantime.

### 5.6 Building carbon-apimgt

Prerequisites:

| Tool | Version | Why |
|---|---|---|
| Java | **JDK 21** (Adoptium Temurin recommended) | Required by APIM 4.6.0 (current stable) and 4.7.0. The carbon-apimgt master branch builds and runs on JDK 21. |
| Apache Maven | 3.6.x or 3.8.x | Build tool |
| Git | any recent | Source control |

Why JDK 21: the WSO2 APIM 4.6.0 release notes (and 4.7.0-Beta) explicitly state JDK 21 as the runtime. The carbon-apimgt master branch tracks the latest APIM release, so the build JDK matches the runtime JDK. **Older JDKs will not work** — the source uses Java 17+ language features (records, sealed types, pattern matching) and the bytecode targets JDK 21. If you've been working with JDK 11 from a tutorial published before mid-2025, that information is stale.

Verify:
```bash
java -version    # openjdk version "21.x"
mvn -version     # 3.6+ AND "Java version: 21.x"
```

If Java is wrong:
```bash
# macOS
brew install --cask temurin@21
export JAVA_HOME=$(/usr/libexec/java_home -v 21)
# Ubuntu/Debian
sudo apt-get install openjdk-21-jdk
export JAVA_HOME=/usr/lib/jvm/java-21-openjdk-amd64
# Other Linux distros — download the tarball from https://adoptium.net/temurin/releases/?version=21
```

To switch between JDK versions when working across multiple projects, use [SDKMAN!](https://sdkman.io/):
```bash
curl -s "https://get.sdkman.io" | bash
sdk install java 21.0.5-tem
sdk use java 21.0.5-tem
```

**Initial full build** (downloads ~2 GB of dependencies, takes 30–60 minutes):

```bash
cd ~/wso2/carbon-apimgt
export MAVEN_OPTS="-Xmx4g -XX:MaxMetaspaceSize=512m"
mvn clean install -Dmaven.test.skip=true
```

Skip tests on the first build to save time. After it succeeds once, you have all dependencies cached.

If it fails — clear cache and retry:
```bash
rm -rf ~/.m2/repository/org/wso2
mvn clean install -Dmaven.test.skip=true
```

**Module-only fast iteration** (after the first full build):

When you've changed only the admin v1 module, rebuild just that:

```bash
mvn clean install -pl components/apimgt/org.wso2.carbon.apimgt.rest.api.admin.v1 -am -Dmaven.test.skip=true
```

`-pl` = "project list" (just this module). `-am` = "also make" (build dependencies if they changed). Typically 1–3 minutes.

The `clean` is critical when changing `admin-api.yaml`. The OpenAPI codegen plugin uses incremental compilation; without `clean` your YAML changes may not regenerate the JAX-RS interface and DTOs.

**Build with tests** (REQUIRED before opening a PR):

```bash
mvn clean install
```

This runs all unit tests across the repo. Takes longer; budget 60–90 minutes total.

**Static analysis** (run before pushing — these run in CI on every PR):

```bash
# Checkstyle
mvn checkstyle:check

# SpotBugs
mvn spotbugs:check

# Semgrep (requires Python and `pip install semgrep`)
semgrep --config semgrep.yml components/apimgt/org.wso2.carbon.apimgt.rest.api.admin.v1
```

If any of these flag issues in your code, fix the root cause. Don't add suppression rules.

### 5.7 Building apim-apps

Prerequisites:

| Tool | Version | Why |
|---|---|---|
| Node.js | 22.x LTS | Repository requirement (current as of 2026) |
| npm | included with Node 22 | Package manager |
| Apache Maven | 3.x | Required only for the Maven layer (`mvn clean install`) |

Verify:
```bash
node --version    # v22.x.x
npm --version     # 10.x or higher
```

If Node is wrong, use `nvm`:
```bash
nvm install 22
nvm use 22
```

**Quick start: dev server with hot reload**

This is the fastest workflow for UI development. Requires a separately running APIM server (download a release pack from wso2.com/api-manager).

```bash
# Start the APIM server in another terminal
cd ~/wso2am-4.x.x/bin
./api-manager.sh

# Then in your work terminal:
cd ~/wso2/apim-apps/portals/admin/src/main/webapp
npm ci          # Clean install matching package-lock.json exactly
npm start       # Dev server with hot reload at http://localhost:8083/admin
```

Login: admin / admin.

`npm ci` is preferred over `npm install` for reproducible builds. It uses `package-lock.json` exactly and never modifies it.

**Production build** (REQUIRED before opening a PR):

```bash
cd ~/wso2/apim-apps/portals/admin/src/main/webapp
npm run build:prod
```

This is the optimized, minified build. If it fails, your PR will be rejected by CI.

**Lint and tests**:

```bash
# ESLint (must pass)
npm run lint
# Scope to your changed files if existing repo has noisy warnings:
npm run lint -- src/app/components/Governance/UnmanagedApis/

# Cypress integration tests
cd ~/wso2/apim-apps/tests
npm install
npm run test           # headless mode (CI)
npm run test:gui       # headed mode (debugging)
npm run test:dev       # interactive mode
```

**Maven build** (builds all 3 portals into `.war` files for deployment into APIM):

```bash
cd ~/wso2/apim-apps
mvn clean install -Dmaven.test.skip=true
```

Skip this until you're ready to deploy into a running APIM. The dev server (`npm start`) is sufficient for most development.

**Deploying your apim-apps build into a running APIM**:

After `mvn clean install`, the admin portal `.war` is at:

```
portals/admin/target/admin.war
```

Copy it to your APIM:

```bash
cp portals/admin/target/admin.war ~/wso2am-4.x.x/repository/deployment/server/webapps/
# APIM auto-deploys on file change. Check logs.
```

### 5.8 Testing the full stack — product-apim integration

For end-to-end testing of both forks, you'll need to build the APIM distribution from `product-apim` with your version bumps. This is per the WSO2 contribution guide.

Clone `product-apim`:
```bash
cd ~/wso2
git clone https://github.com/wso2/product-apim.git
```

After your `mvn clean install` on carbon-apimgt and/or apim-apps:

1. Read the `<version>` from your local repo's `pom.xml` (e.g., `9.32.151-SNAPSHOT`).
2. Edit `~/wso2/product-apim/all-in-one-apim/pom.xml`:
   - For carbon-apimgt changes: update `<carbon.apimgt.version>`
   - For apim-apps changes: update `<carbon.apimgt.ui.version>`
3. Build product-apim:
```bash
cd ~/wso2/product-apim/all-in-one-apim
mvn clean install -Dmaven.test.skip=true
```
4. The pack appears in `modules/distribution/product/target/wso2am-4.x.x-SNAPSHOT.zip`. Extract it, start the server, test.

Run with tests for the actual PR validation:
```bash
mvn clean install
```
This runs integration tests and takes 2–3 hours. Required before submitting a PR.

### 5.9 Pre-PR checklist

Before opening a PR to either repo:

```
[ ] Synced with upstream (git fetch + rebase, no conflicts)
[ ] Branch name follows convention (feat/api-discovery-governance or feat/api-discovery-governance-ui)
[ ] Full build with tests passes locally (mvn clean install — both repos)
[ ] product-apim integration build passes (mvn clean install in all-in-one-apim)
[ ] No new Maven modules
[ ] No new OSGi bundles
[ ] No new npm dependencies
[ ] No DB schema migrations
[ ] All commits have my real name and email (verify with git log --pretty=fuller)
[ ] No commits have AI/Claude metadata
[ ] No claude/ files in the diff (verify with git status)
[ ] PR title follows [API Discovery Governance] feat: ... format
[ ] PR description has Purpose, Approach, Tests, Screenshots
[ ] At least one screenshot of the working feature
```

### 5.10 Opening the PR

Push your branch:
```bash
git push origin feat/api-discovery-governance         # or feat/api-discovery-governance-ui
```

Go to the upstream repo on GitHub. GitHub usually offers a banner: "Compare & pull request" pointing at your recent push. Click it.

PR title format:
- carbon-apimgt: `[API Discovery Governance] feat: backend BFF for unmanaged API discovery`
- apim-apps: `[API Discovery Governance UI] feat: Unmanaged APIs tab under Governance`

PR base branch:
- carbon-apimgt PR base: `wso2/carbon-apimgt:master`
- apim-apps PR base: `wso2/apim-apps:main`

PR description template:

```markdown
## Purpose

<short paragraph: what does this PR do, why does it matter>

This is part of a feature spanning carbon-apimgt + apim-apps. The other PR is at <link>.

## Approach

<short list of the technical approach — file structure, patterns followed, key choices>

- Followed existing pattern: <reference an existing similar resource/feature>
- New paths under `/governance/discovery/*` in admin-api.yaml
- Implemented `DiscoveryApiServiceImpl` in the existing admin v1 module
- Added a small HTTP client (`DiscoveryApiServerClient`) using bearer token auth
- 2 new OAuth2 scopes registered in `tenant-conf.json`

No new Maven modules. No new OSGi bundles. No database changes.

## Tests

- Unit tests for `DiscoveryApiServiceImpl` (X% coverage)
- Manually tested end-to-end against APIM 4.x distribution with a running ADS instance
- Screenshots attached

## Screenshots

<paste screenshots>

## Related

- Frontend PR: <will link once opened>
```

The "Related" section links to a sibling PR or issue. Do NOT link to internal design docs or `claude/` paths — those are local.

### 5.11 During review

A WSO2 maintainer (auto-assigned via `CODEOWNERS`) will leave comments. Plus CodeRabbit AI may auto-comment.

Respond to every comment. When you push fixes:
```bash
git add .
git commit -m "fix: <what you changed in response to feedback>"
git push origin feat/api-discovery-governance
```

Don't force-push during review (makes the diff hard to follow). Use force-push only if the reviewer explicitly asks for a rebase or squash.

### 5.12 After merge

```bash
# In the fork — sync default branch
cd ~/wso2/carbon-apimgt
git checkout master
git fetch upstream
git merge upstream/master --ff-only
git push origin master

# Delete the merged feature branch
git branch -d feat/api-discovery-governance
git push origin --delete feat/api-discovery-governance
```

### 5.13 Continuous upstream sync rhythm — keeping PRs conflict-free

This subsection consolidates the cadence rules. **The rules apply ONLY to the two WSO2 forks (`carbon-apimgt`, `apim-apps`) — they do NOT apply to the ADS daemon repo (`Kidhurshan/wso2-api-discovery-server`), which has no upstream.** For the ADS repo, just push to `origin/main` after each round merges; nothing else.

The principle behind everything below: **the longer you wait between rebases, the more it costs.** Rebasing 5 commits is trivial. Rebasing 50 produces conflicts. Rebasing 200 produces conflicts you can't resolve without context you've lost. WSO2's `master` and `main` move fast enough that "weekly" is already late.

#### 5.13.1 Daily fetch (the cheapest possible discipline)

Every morning, in every WSO2 fork you have checked out, run:

```bash
git fetch upstream
```

That's it. It downloads new refs from `wso2/carbon-apimgt` or `wso2/apim-apps` without changing any of your branches. Costs ~2 seconds. Tells you exactly how far ahead upstream has moved (`git log HEAD..upstream/master --oneline | wc -l`). If the answer is "0", you're in sync; if it's "30", you have rebasing to do soon.

This is the single habit that prevents the "where did all this drift come from?" surprise at PR time.

#### 5.13.2 Sync the default branch before every new piece of work

Already covered in §5.4 — `git fetch upstream && git merge upstream/master --ff-only && git push origin master`. Run it before creating any new feature branch and before resuming work on an existing one after a break of more than a day.

#### 5.13.3 Rebase the feature branch every 2–3 days

This is the rule most contributors get wrong. They rebase weekly, then biweekly, then "I'll deal with it before the PR" — and the PR rebase becomes a nightmare. WSO2 maintainers expect feature branches to track current `master` (or `main`). Don't fall behind.

Cadence: **at minimum every 2–3 working days**, more often if upstream is particularly active that week.

```bash
# carbon-apimgt feature branch
cd ~/wso2/carbon-apimgt
git checkout feat/api-discovery-governance
git fetch upstream
git rebase upstream/master

# If conflicts: resolve, then
git add <resolved-files>
git rebase --continue

# Force-push your fork's branch (your branch only — never the default branch)
git push --force-with-lease origin feat/api-discovery-governance
```

For apim-apps, same pattern but `upstream/main` instead of `upstream/master`.

`--force-with-lease` is the safe variant of `--force`. It refuses to push if someone else (or another machine of yours) added commits to the remote branch in the meantime. Always use it; never plain `--force`.

**Rebase, never merge into the feature branch.** WSO2 PRs use a linear-history rebase workflow — merge commits in the feature branch's history will be flagged in review. If you accidentally merged, reset and redo with rebase.

#### 5.13.4 Pre-commit upstream check

Before every commit on a feature branch, a 5-second habit:

```bash
git fetch upstream
git log HEAD..upstream/master --oneline | head -20
```

If the upstream output mentions something that overlaps with what you're about to commit (touching the same files, same area), check it first. Sometimes someone already fixed the bug you're fixing. Sometimes there's a related refactor that changes how your code should look. Five seconds of `git log` saves an hour of "oh, I needed to know about that."

#### 5.13.5 Pre-PR final rebase (mandatory)

The last action before opening a PR is a final rebase against current upstream:

```bash
git fetch upstream
git rebase upstream/master    # or upstream/main for apim-apps
git push --force-with-lease origin feat/api-discovery-governance
```

Then immediately open the PR. Anything between the final rebase and the PR submission is an opportunity for upstream to move and your "rebase clean" claim to no longer be true.

The PR description should explicitly state: "Rebased on upstream/master at <SHA>". This signals to reviewers that the PR is ready, not stale.

#### 5.13.6 During code review — keep rebasing preemptively

Reviews can take 1–4 weeks at WSO2. During that window, upstream moves. If your branch goes stale, the reviewer will eventually comment "please rebase" — at which point you've added a round-trip to the review cycle.

Faster path: rebase preemptively every 3–4 days during review. The reviewer sees the branch staying current, doesn't need to ask, and the PR closes faster.

```bash
# During review — same command as §5.13.3
git fetch upstream
git rebase upstream/master
git push --force-with-lease origin feat/api-discovery-governance
```

But: don't force-push to "tidy up" commits during review (squashing, reordering, rewording). The reviewer is looking at specific commits and lines. Rewriting history mid-review makes their feedback harder to track. Save squashing for after approval, when the maintainer asks (or for the merge-commit message).

#### 5.13.7 Conflict resolution — when rebasing isn't clean

When `git rebase upstream/master` produces conflicts:

1. **Don't panic and don't `git rebase --abort` reflexively.** A 2–3 file conflict is normal after several days of upstream activity.
2. Resolve each conflicted file. Read both sides carefully. Prefer the upstream version for code unrelated to your feature; prefer your version for the feature-specific lines.
3. `git add <file>` for each resolved file. Then `git rebase --continue`.
4. Run the build (`mvn clean install -DskipTests` for carbon-apimgt; `npm run build` for apim-apps) before pushing. A "successful" rebase that breaks the build is worse than a failed rebase.
5. Force-push: `git push --force-with-lease origin <branch>`.

If the conflict is large (10+ files, or fundamental architectural shift in upstream), pause and think. Consider whether your approach still fits the codebase. Sometimes upstream has refactored exactly the area you're working on, and the right move is to redesign your patch around the new shape rather than mechanically resolve line-by-line.

#### 5.13.8 The pre-push checklist

Every push to a feature branch (any branch on your fork that's destined for a PR) goes through these four checks:

```bash
# 1. Rebased on current upstream
git fetch upstream
git log HEAD..upstream/master --oneline    # Empty = up to date

# 2. Build passes
mvn clean install -DskipTests   # or npm run build for apim-apps

# 3. No AI/Claude metadata anywhere in commits
git log --pretty=fuller upstream/master..HEAD | grep -iE "(claude|co-authored|generated.with|anthropic|copilot|cursor)"
# Expected: empty

# 4. No trailing whitespace, no debug prints
git diff upstream/master..HEAD | grep -E "^\+.*\s+$|^\+.*console\.log|^\+.*System\.out\.println|^\+.*fmt\.Print" | head -5
# Expected: empty (or only intentional logging)
```

If any of the four fail, fix before pushing. WSO2 reviewers will catch any one of them on first pass.

#### 5.13.9 Quick reference — the daily git rhythm

| When | Action | Cost |
|---|---|---|
| Start of every session | `git fetch upstream` in each WSO2 fork | ~2 seconds |
| Before any new feature work | §5.4 default-branch sync | ~10 seconds |
| Every 2–3 days during active development | `git fetch upstream && git rebase upstream/master` on feature branch | 30 seconds–5 minutes |
| Before every commit | `git fetch upstream && git log HEAD..upstream/master --oneline` | 5 seconds |
| Just before opening the PR | Final rebase + force-push (§5.13.5) | 1 minute |
| Every 3–4 days during code review | Preemptive rebase + force-push | 1 minute |
| After PR merges | `git checkout master && git pull && git branch -d <feature-branch>` | 10 seconds |

**The economics:** these habits sum to maybe 5 minutes/day. Skipping them and resolving the resulting conflicts at PR time costs hours per PR.

---

## 6. WSO2 coding standards (carbon-apimgt — Java)

Follow the patterns of the existing throttling, governance, and tenants resources in `org.wso2.carbon.apimgt.rest.api.admin.v1`. The closest analogue to copy is `ThrottlingApiServiceImpl`.

**Style and format:**

- Java 21 source, Java 21 target. Match `maven.compiler.source` / `maven.compiler.target` from the parent `pom.xml`; do not pin a different version in your module.
- Modern Java features are fine where they read better — `var` for local inference, records for immutable DTOs, switch expressions, pattern matching, text blocks. Don't reach for them gratuitously, but don't avoid them either.
- 4-space indentation. Spaces, not tabs.
- 120-character maximum line length.
- Imports: ordered alphabetically; no wildcard imports; star imports forbidden.
- Use existing `import` patterns — e.g., `org.wso2.carbon.apimgt.api.APIManagementException` for business errors.
- Every public class and method gets a Javadoc.
- Every class header includes `@since` Javadoc. Use the next-release version from the parent `pom.xml` `<version>` element (e.g., `@since 4.7.0` if that's the current `-SNAPSHOT` version on `master` when you submit the PR).

**Naming:**

- Class names: PascalCase, descriptive, no abbreviations (`DiscoveryApiServiceImpl`, not `DiscApiSvcImpl`).
- Method names: camelCase, verb-first (`getDiscoverySummary`, not `discoverySummaryGet`).
- Boolean methods: `is`/`has`/`can` prefix (`isInternal`, `hasMatchingIdentity`).
- Constants: `UPPER_SNAKE_CASE` (`MAX_RETRY_COUNT`).
- Package names: lowercase, singular where possible.
- DTO names: end with `DTO` (`DiscoveredAPIDTO`).
- Service interface names: end with `Service` (`DiscoveryApiService`).
- Implementation class names: end with `ServiceImpl` (`DiscoveryApiServiceImpl`).

**Logging:**

- Use Apache Commons Logging: `private static final Log log = LogFactory.getLog(DiscoveryApiServiceImpl.class);`
- Log format: `log.error("description of what failed", e);` — exception is the second arg, not part of the string.
- Never log secrets, tokens, passwords, or PII.

**Error handling:**

- Throw `APIManagementException` for business errors.
- Use `RestApiUtil.handleInternalServerError(...)`, `handleResourceNotFoundError(...)`, etc., for REST surface errors.
- Never swallow exceptions silently.
- Never use `e.printStackTrace()`.

**Don't:**

- Add new Maven modules.
- Add new OSGi bundles.
- Add new dependencies to `pom.xml` (Apache HttpClient, Jackson, Log4j are already transitive — use them).
- Reformat existing files.
- Change `checkstyle.xml`, `spotbugs-exclude.xml`, or `semgrep.yml`.

---

## 7. WSO2 coding standards (apim-apps — JSX)

Follow the patterns of the existing `Governance/Compliance/` feature in `portals/admin/src/main/webapp/source/src/app/components/`. That's the closest analogue.

**Stack — locked, no deviations:**

- React 17, function components with hooks.
- MUI v4 (`@material-ui/core`, `@material-ui/icons`, `@material-ui/styles`).
- Recharts for all charts (already a transitive dependency).
- `react-intl` for all user-facing strings (no hardcoded English).
- Auto-generated Swagger client through `data/DiscoveryApi.js`, mirroring `data/ThrottlingApi.js`.

**Style:**

- ESLint config from the repo. Run `npm run lint` before commit.
- 4-space indentation. Single quotes for strings. Trailing commas on multi-line arrays/objects.
- File naming: `PascalCase.jsx` for components, `camelCase.js` for utilities.

**Imports:**

```jsx
// CORRECT — MUI v4
import { Box, Card, Typography } from '@material-ui/core';
import { makeStyles } from '@material-ui/core/styles';
import SearchIcon from '@material-ui/icons/Search';

// WRONG — MUI v5 imports do NOT exist in this project
// import { Box } from '@mui/material';
```

If you accidentally use v5 imports, the build fails at the bundler step. Always verify imports.

**i18n is mandatory:**

```jsx
import { useIntl, FormattedMessage } from 'react-intl';

// CORRECT
const intl = useIntl();
const title = intl.formatMessage({ id: 'Discovery.title', defaultMessage: 'Unmanaged APIs' });

// WRONG
const title = 'Unmanaged APIs';   // hardcoded string — flagged in review
```

Translation keys go in `portals/admin/src/main/webapp/source/src/locales/en/admin.json` under the `Discovery.*` namespace.

**Don't:**

- Add new npm dependencies. None.
- Add new dev dependencies. None.
- Change webpack config.
- Change `.eslintrc` or `.prettierrc`.
- Introduce TypeScript in new files (the admin portal is JSX).
- Introduce Redux, Zustand, MobX, or any new state management library. Use React local state + localStorage for preferences.
- Introduce new charting libraries.
- Introduce new icon libraries.
- Reformat existing files.

**Browser-side state strategy** (per intern constraints — no APIM DB writes):

| Layer | Use for | Survives |
|---|---|---|
| React local state (`useState`) | Filter selections, sort order, pagination | Component lifetime |
| `localStorage` | User preferences (column visibility, page size) | Browser session, persistent |
| `sessionStorage` | Not used in v1 | n/a |
| `IndexedDB` | Not used in v1 | n/a |

localStorage key convention: `apim.admin.governance.unmanaged_apis.prefs.v1`. Versioned suffix `.v1` — bump to `.v2` if shape changes; old data is silently ignored.

---

## 8. Git discipline (CRITICAL)

This is intern work submitted as PRs to WSO2. The commits, branch names, PR descriptions, and contributor metadata must be clean and professional. Three rules.

### 8.1 No Claude or AI metadata anywhere — ever

Commits must not contain:

- `Co-Authored-By: Claude <noreply@anthropic.com>` lines
- "Generated with Claude" or "Generated with AI" mentions in commit message body or PR description
- Any AI tool attribution in code comments
- Any reference to "Claude Code", "Anthropic", "Cursor", "Copilot", or similar tools

**Configure git locally before any commit:**

```bash
git config user.name "Kidhurshan Sivasubramaniam"
git config user.email "<your-real-email-on-github>"
git config commit.gpgsign false
```

**Verify before EVERY push:**

```bash
git log --pretty=fuller -10 | grep -iE "(claude|co-authored|generated.with|anthropic|copilot|cursor)"
# Expected: empty output
```

If anything matches, the commit needs to be rewritten:

```bash
# Last commit only
git commit --amend
# Older commit (interactive rebase)
git rebase -i HEAD~5
# In the editor, change "pick" → "reword" for the bad commit, save, edit message, save
```

If the issue is the author or committer (not just the message), the rewrite needs `--reset-author`:

```bash
git rebase -i HEAD~5
# In editor: change "pick" → "edit" for the bad commit
# At the rebase prompt:
git commit --amend --reset-author --no-edit
git rebase --continue
git push origin <branch> --force-with-lease
```

If anything has already been pushed with bad metadata and someone might have pulled, ask for help — the cleanup with `git filter-repo` is messy.

### 8.2 Commit message conventions

Conventional commits prefix. Format:

```
<type>: <short description in imperative mood>

<optional longer body explaining what and why, not how>

<optional footer for issue references>
```

Types: `feat`, `fix`, `refactor`, `test`, `docs`, `chore`, `perf`, `style`.

**Good examples:**

```
feat: add discovery summary endpoint for governance dashboard

Adds GET /governance/discovery/summary returning aggregate
counts of discovered APIs by classification (shadow, drift)
and reachability (external, internal). Implements path entry
in admin-api.yaml and DiscoveryApiServiceImpl.
```

```
fix: handle null endpointConfig in publisher resolver

The deployment-aware resolver assumed endpointConfig was always
populated. Some legacy APIs in production have null configs.
Returns env_kind=unknown with a warning instead of NPE.
```

```
test: add unit tests for DiscoveryApiServiceImpl

Mocks the discovery client and tests the four endpoints
(summary, list, detail, untrafficked) with happy-path
and error cases. Coverage 87% for the new class.
```

**Bad examples — do not write:**

```
update files
```

```
feat: phase 4 work
Co-Authored-By: Claude <noreply@anthropic.com>
```

```
WIP — will fix later
Generated with assistance from AI tools.
```

### 8.3 The `claude/` directory is local-only

Spec files, design docs, and Claude Code working notes never leave your local disk.

`.gitignore` enforces this:

```
claude/
```

The `CLAUDE.md` file at the repo root is explicitly NOT excluded (it's not inside `claude/`). It's the entry point for Claude Code and is intentionally pushed.

Before each commit:

```bash
git status --short | grep -iE "(claude|specs)" | grep -v "^.. CLAUDE.md$"
# Expected: empty output
# If anything else appears, fix .gitignore or unstage the file
```

If you accidentally pushed `claude/specs/*.md` content (rare but possible if `.gitignore` was misconfigured):

```bash
# 1. Remove from git tracking but keep locally
git rm -r --cached claude/

# 2. Verify .gitignore has claude/ rule
cat .gitignore | grep "^claude/$"

# 3. Commit the deletion
git commit -m "chore: remove tracked spec files (now gitignored)"
git push origin <branch>

# 4. If the bad files are deep in history, the cleanup needs git filter-repo —
# ask for help, don't try to fix solo
```

---

## 9. Branching strategy summary

### 9.1 ADS repo (`Kidhurshan/wso2-api-discovery-server`)

Personal repo. Use feature branches per implementation round, merge to `main`, push.

```
main                          ← stable, tested code only
├── feat/foundation           ← Round 1: config, logging, models, store, schema
├── feat/discovery            ← Round 2: Phase 1 traffic discovery
├── feat/managed              ← Round 3: Phase 2 managed API sync
├── feat/comparison           ← Round 4: Phase 3 classification engine
├── feat/bff                  ← Round 5: BFF REST server
└── feat/hardening            ← Round 6: circuit breakers, health, deploy
```

Workflow per round:

```bash
git checkout main
git pull origin main
git checkout -b feat/<round-name>
# ... implement, test against TechMart ...
git add .
git commit -m "feat: <description>"
git checkout main
git merge feat/<round-name>
git push origin main
git branch -d feat/<round-name>
```

Don't use force-push on `main`. Force-push on feature branches is fine if needed before merge.

### 9.2 WSO2 forks

Long-lived feature branches off the upstream default branch. Detail in §5.5.

```
upstream/master (carbon-apimgt) or upstream/main (apim-apps)
   ↓ rebased regularly via git fetch + git rebase
fork/master or fork/main          ← kept in sync with upstream
   ↓ branched from
feat/api-discovery-governance     ← carbon-apimgt feature branch
feat/api-discovery-governance-ui  ← apim-apps feature branch
```

---

## 10. Implementation order

Three workstreams, sequenced. After every round, test against TechMart and write a test result report to `test/results/<round>_test_report.md` (this directory IS pushed — visible to mentors).

### Workstream A — ADS daemon (Go)

| Round | Branch | Build | Test |
|---|---|---|---|
| 1 — Foundation | `feat/foundation` | config, logging, models, store, main.go, schema/ | `make build`, `ads --validate`, verify migrations |
| 2 — Discovery | `feat/discovery` | deepflow client, normalizer, merger, engine loop | TechMart traffic gen → verify `ads_discovered_apis` |
| 3 — Managed sync | `feat/managed` | apim auth + publisher client, resolver, dns cache | Verify `ads_managed_apis` matches APIM, soft-delete works |
| 4 — Comparison | `feat/comparison` | SQL classifier, view refresh, freshness guard | Verify `ads_classifications` matches ground truth |
| 5 — BFF | `feat/bff` | REST server, bearer token verification, pagination | curl every endpoint, verify shapes |
| 6 — Hardening | `feat/hardening` | breakers, graceful shutdown, retention, deploy artifacts | Full E2E + outage recovery |

### Workstream B — carbon-apimgt fork

Starts when Workstream A reaches Round 5 (real BFF data available). Branch: `feat/api-discovery-governance`.

| Round | Build | Test |
|---|---|---|
| 7 — admin-api.yaml extension | Add 4 path entries + 5 schemas under `/governance/discovery/*` | `mvn clean install -pl ...admin.v1 -am`. Confirm DTO and JAX-RS interface generate. |
| 8 — BFF implementation | `DiscoveryApiServiceImpl`, `DiscoveryApiServerClient`, `DiscoveryMappingUtil`, wire DTOs, config in `deployment.toml.j2`, scopes in `tenant-conf.json` | Unit tests with mocked client. Integration test: deploy `.war` into APIM, hit endpoint via curl, confirm responses. |

### Workstream C — apim-apps fork

Starts when Workstream B reaches Round 7 (admin-api.yaml has the new paths so the auto-generated Swagger client has the endpoints). Branch: `feat/api-discovery-governance-ui`.

| Round | Build | Test |
|---|---|---|
| 9 — Empty tab | `UnmanagedApis/` directory with placeholder. Register route in `RouteMenuMapping.jsx`. Add i18n strings. | `npm start`, verify tab appears under Governance with placeholder content. |
| 10 — List view | `data/DiscoveryApi.js`, `UnmanagedApisList.jsx`, `ApiCoverageCard.jsx`, `BreakdownCard.jsx`, `FindingsTable.jsx`, `FindingsFilters.jsx` | Verify list renders against TechMart with daemon running. Filters work, pagination works. |
| 11 — Detail view | `UnmanagedApiDetail.jsx`, `IdentityPanel.jsx`, `EvidencePanel.jsx`, `ReasonPanel.jsx`. Plain-English "Why this is a finding" for all 4 cases. | Click each row, verify detail page renders correctly. |
| 12 — Polish | Loading skeletons, empty states, error states, accessibility (aria labels, focus rings) | Lighthouse a11y score parity with existing Compliance tab |

---

## 11. Critical project constraints

These constraints come from the intern brief and shape every design decision. Violating any invalidates the work.

- **No WSO2 APIM database changes.** No new tables in `WSO2AM_DB`. No schema migrations. Discovery state lives only in ADS's own PostgreSQL.
- **Small, idiomatic WSO2 changes only.** No new Maven modules. No new OSGi bundles. No new npm dependencies. New code lives inside existing modules following existing patterns. The PR must be reviewable in one sitting.
- **Browser-side state where possible.** User filter preferences, sort order, column visibility — all `localStorage`. Server-side state stays in ADS's PostgreSQL.
- **Match WSO2 design language.** No custom UI components, no new icon sets, no new charting library. MUI v4, Recharts, react-intl, the auto-generated Swagger client.
- **Universal system.** TechMart is the test environment ONLY. Never hard-code any TechMart-specific value (hostname, port, namespace, SKU pattern, service name) in source code.

---

## 12. Resilience features (Go daemon)

ADS runs unattended, surviving VM/pod restarts:

- **DB startup retry** — `store.ConnectWithRetry()` retries 30 times with exponential backoff (~5 min).
- **HTTP request retry** — `httputil.DoWithRetry()` — 3 attempts with backoff + jitter for transient errors. Idempotency guard: only GET/HEAD/PUT/DELETE/OPTIONS retried; POST/PATCH never.
- **Circuit breakers** — phase-level (discovery, managed) with exponential backoff. Exponent capped at 20 to prevent float64 overflow.
- **Phase 3 freshness guard** — comparison runs only when Phase 2 success was within 3× the managed poll interval. Prevents false Shadow classifications when managed data is stale.
- **Non-fatal DeepFlow init** — if DeepFlow is unavailable at startup, daemon runs in degraded mode (discovery disabled, BFF still serves cached state).
- **Graceful shutdown** — SIGTERM/SIGINT handling with context cancellation. Current cycle completes before exit.
- **Health probes** — `/healthz` (liveness) and `/readyz` (readiness, 503 when any breaker OPEN).
- **OAuth2 expiry guard** — if APIM returns `expires_in ≤ 60`, buffer reduced to `expires_in/3`.
- **Systemd** — `Restart=always`, `After=network-online.target`, `StartLimitBurst=10`.
- **Kubernetes** — startup probe (5 min window), liveness, readiness, `terminationGracePeriodSeconds: 60`. Lease-based leader election for multi-replica deployments.

Detail in `claude/specs/operations_guide.md`.

---

## 13. Critical warnings

**UNIVERSAL SYSTEM.** TechMart is the test environment ONLY. All external references come from `config.toml` or runtime discovery.

**DON'T ASSUME.** If the spec doesn't cover something, stop and ask. Don't invent behavior. Every SQL query, algorithm, and edge case is in the spec files.

**DON'T HALLUCINATE.** If unsure about a DeepFlow field name, APIM API response structure, or PostgreSQL behavior — check the spec. Don't guess.

**SECURITY FIRST.** Parameterized SQL only. No string concatenation in queries. Validate all external input. Never log secrets.

**WSO2 PR DISCIPLINE.** When working on the carbon-apimgt or apim-apps forks: every change follows existing patterns. No new modules. No new dependencies. No design system deviations.

**SYNC UPSTREAM CONTINUOUSLY.** Run `git fetch upstream` at the start of every work session in each WSO2 fork. Rebase your feature branch onto `upstream/master` (carbon-apimgt) or `upstream/main` (apim-apps) every 2–3 days during active development, and once more immediately before opening the PR. Skipping this discipline is the single biggest cause of PR conflicts. Full cadence rules in §5.13.

**NO CLAUDE/AI TRACES.** Never include "Generated with Claude," "Co-Authored-By: Claude," or any AI-attribution in commit messages, PR descriptions, or code comments. Verify before every push with the grep command in §8.1.

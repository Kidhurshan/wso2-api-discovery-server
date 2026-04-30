# WSO2 API Discovery Server: User Guide

This guide walks you through setting up and running the WSO2 API Discovery Server end-to-end. Follow the stages in order and you will have a working installation that surfaces unmanaged APIs inside the WSO2 API Manager Admin Portal.

A more in-depth design and architecture document is provided separately in the project's reference materials.

---

## Table of Contents

1. [Overview](#1-overview)
2. [Architecture](#2-architecture)
3. [Prerequisites](#3-prerequisites)
4. [Stage 1: Set Up DeepFlow](#4-stage-1-set-up-deepflow)
5. [Stage 2: Set Up the API Discovery Server](#5-stage-2-set-up-the-api-discovery-server)
6. [Stage 3: Set Up WSO2 API Manager](#6-stage-3-set-up-wso2-api-manager)
7. [Run the System and View Results](#7-run-the-system-and-view-results)
8. [Viewing Logs](#8-viewing-logs)
9. [Troubleshooting](#9-troubleshooting)

---

## 1. Overview

The WSO2 API Discovery Server observes runtime HTTP traffic in your enterprise, compares the observed paths against APIs registered in WSO2 API Manager, and reports the gaps inside the API Manager Admin Portal as a new "Unmanaged APIs" tab in the Governance section.

### Classifications

| Classification | Meaning |
|---|---|
| **Shadow** | The service has no managed APIs in WSO2 API Manager. The discovered path is on a backend WSO2 API Manager has no visibility into. |
| **Drift** | The service has managed APIs, but this specific path is not declared. |
| **Internal** (modifier) | Optional. Flags traffic that travelled only inside the cluster. Combines with Shadow or Drift. |

### Key principles

* **Non-intrusive.** No code change to your services. No agent in the application runtime. No write access to the WSO2 API Manager database.
* **Universal.** Works for Kubernetes services, virtual machines, and bare-metal hosts.
* **Read-only on the WSO2 API Manager side.** The only output is a REST surface that the Admin Portal reads through a small Backend-for-Frontend layer.

---

## 2. Architecture

### High-level system view

```mermaid
flowchart LR
    User(["User"])
    Apps["Service Pods<br/>and VMs"]
    DF["DeepFlow"]
    DS["API Discovery<br/>Server"]
    DB[("PostgreSQL")]
    APIM["WSO2 API Manager<br/>(Admin Portal + BFF)"]

    Apps -. "HTTP traffic" .-> DF
    DF -- "Flow data" --> DS
    APIM == "Managed APIs" ==> DS
    DS == "Findings" ==> APIM
    DS --- DB
    User --> APIM

    classDef src fill:#dbeafe,stroke:#1e40af,stroke-width:2px,color:#0f172a
    classDef cap fill:#fef3c7,stroke:#a16207,stroke-width:2px,color:#0f172a
    classDef core fill:#fed7aa,stroke:#c2410c,stroke-width:2px,color:#0f172a
    classDef store fill:#e9d5ff,stroke:#6b21a8,stroke-width:2px,color:#0f172a
    classDef ux fill:#d1fae5,stroke:#065f46,stroke-width:2px,color:#0f172a
    classDef person fill:#f3e8ff,stroke:#581c87,stroke-width:2px,color:#0f172a

    class Apps src
    class DF cap
    class DS core
    class DB store
    class APIM ux
    class User person
```

### How the system works (abstract flow)

The diagram below shows the complete flow at a high level. The Discovery Server runs cycles in the background to gather and classify data; the Admin Portal queries on demand.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant Portal as Admin Portal
    participant Disco as Discovery Server
    participant DF as DeepFlow
    participant APIM as WSO2 API Manager
    participant DB as PostgreSQL

    Note over Disco,DB: Background cycles run continuously
    Disco->>DF: Pull observed traffic (every 5 min)
    Disco->>APIM: Pull managed APIs (every 10 min)
    Disco->>DB: Compare and store classifications

    Note over User,DB: On-demand read when user opens the tab
    User->>Portal: Open "Unmanaged APIs" tab
    Portal->>Disco: Request findings
    Disco->>DB: Read classifications
    DB-->>Disco: Findings
    Disco-->>Portal: JSON response
    Portal-->>User: Render charts and table
```

### Components

| Component | Role |
|---|---|
| **DeepFlow** | Captures HTTP traffic on every node using eBPF. Aggregates and stores it in ClickHouse. |
| **API Discovery Server** | Pulls observed traffic from DeepFlow, pulls managed APIs from WSO2 API Manager, classifies each finding, and serves the result via a REST endpoint. |
| **WSO2 API Manager** | Hosts the Admin Portal. The admin REST module proxies the Discovery Server through a small Backend-for-Frontend. |
| **PostgreSQL** | Persistent state for the API Discovery Server. |

---

## 3. Prerequisites

| Requirement | Version | Purpose |
|---|---|---|
| Kubernetes cluster | 1.24+ | Hosts DeepFlow. Optional host for the Discovery Server. |
| Helm | 3.x | Installs DeepFlow and the Discovery Server. |
| kubectl | matching cluster | Cluster administration. |
| WSO2 API Manager | 4.6.0+ on JDK 21 | Source of managed APIs and host of the Admin Portal. |
| PostgreSQL | 13+ | Discovery Server's persistent store. The bundled installers provision this for you. Required only if you supply your own. |

### Network ports

| Service | Port | Used by |
|---|---|---|
| API Discovery Server BFF | 8443 (TLS) | WSO2 API Manager |
| API Discovery Server health | 9090 | Kubernetes liveness probes |
| WSO2 API Manager Publisher | 9443 (TLS) | API Discovery Server |
| DeepFlow Querier | 30617 (NodePort) | API Discovery Server |
| PostgreSQL | 5432 | API Discovery Server |

---

## 4. Stage 1: Set Up DeepFlow

### 4.1 Install DeepFlow Server

```bash
# Add the official DeepFlow Helm repository
helm repo add deepflow https://deepflowio.github.io/deepflow
helm repo update

# Create a dedicated namespace
kubectl create namespace deepflow

# Install the server stack (server, ClickHouse, MySQL)
helm install deepflow deepflow/deepflow \
    -n deepflow \
    --set global.image.repository=deepflowio/deepflow-server \
    --set global.storageClass=standard

# Wait for the pods to reach Running
kubectl get pods -n deepflow --watch
```

You should see `deepflow-server`, `deepflow-clickhouse`, and `deepflow-mysql` pods reach `Running`.

### 4.2 Install DeepFlow Agents

```bash
# Get the DeepFlow Server NodeIP (use any worker node IP)
SERVER_NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')

# Install agents as a DaemonSet on every node
helm install deepflow-agent deepflow/deepflow-agent \
    -n deepflow \
    --set deepflowServerNodeIP=$SERVER_NODE_IP

# Verify agents are Running on every node
kubectl get pods -n deepflow -l app=deepflow-agent
```

For virtual-machine hosts (no Kubernetes), use the standalone DeepFlow Agent package available from the official DeepFlow website.

### 4.3 Apply the Agent Group Configuration

Save the YAML below as `agent-group-config.yaml`.

```yaml
global:
  communication:
    proactive_request_interval: 60s
    max_escape_duration: 1h
  resource_limits:
    cpu_limit: 1
    memory_limit: 768
  log:
    log_level: INFO

inputs:
  proc:
    enabled: true
  cbpf:
    common:
      capture_packet_size: 65535
  ebpf:
    socket:
      preprocess:
        out_of_order_reassembly_protocols: [HTTP, HTTP2]
      uprobe:
        golang:
          enabled: true
        tls:
          enabled: true

processors:
  request_log:
    application_protocol_inference:
      inference_max_retries: 5
      enabled_protocols: [HTTP, HTTP2]
    filters:
      port_number_prefilters:
        HTTP: "1-65535"
        HTTP2: "1-65535"
    timeouts:
      tcp_request_timeout: 1800s
    tag_extraction:
      http_endpoint:
        extraction_disabled: false
        match_rules:
          - url_prefix: ""
            keep_segments: 16
    tunning:
      payload_truncation: 4096
      session_aggregate:
        max_entries: 65536

outputs:
  flow_log:
    filters:
      l4_capture_network_types: [0]
      l7_capture_network_types: [0, 3]
    aggregator:
      aggregate_health_check_l7_flow_log: true
    throttles:
      l4_throttle: 50000
      l7_throttle: 50000

  flow_metrics:
    filters:
      apm_metrics: true
      npm_metrics: true
```

Apply the configuration through the DeepFlow Server UI:

1. Open the DeepFlow Server UI (typically `http://<server-node-ip>:20418`).
2. Navigate to **DeepFlow > Agent > Agent Group**.
3. Click **Edit Configuration** on the default group.
4. Paste the YAML and click **Save**.

Agents pick up the new configuration within 60 seconds.

### 4.4 Field-by-Field Reference

| Field | Default | Purpose | When to change |
|---|---|---|---|
| `global.resource_limits.cpu_limit` | `1` | Maximum CPU cores per agent. | Raise to 2 or 4 if agents drop traffic. |
| `global.resource_limits.memory_limit` | `768` MiB | Maximum memory per agent. | Raise to 1024 or 2048 on high-throughput nodes. |
| `inputs.ebpf.socket.uprobe.tls.enabled` | `true` | Whether to attach uprobes to user-space TLS libraries. | Keep `true` to maximise HTTPS visibility. |
| `processors.request_log.application_protocol_inference.enabled_protocols` | `[HTTP, HTTP2]` | Which application protocols to parse. | Always include `HTTP` and `HTTP2`. |
| `processors.request_log.filters.port_number_prefilters.HTTP` | `1-65535` | Destination ports the agent inspects for HTTP. | Narrow to specific ranges to reduce CPU. |
| **`processors.request_log.tag_extraction.http_endpoint.match_rules[].keep_segments`** | **`16`** | **How many path segments are stored. Lower values truncate deep URLs.** | **Keep this value high. Use 16. Lowering it causes deep URLs to be truncated and the Discovery Server's path normaliser will receive incomplete paths.** |
| `processors.request_log.tunning.payload_truncation` | `4096` | Bytes of HTTP payload retained. | Default is fine. The Discovery Server does not depend on payloads. |
| `outputs.flow_log.filters.l7_capture_network_types` | `[0, 3]` | Network types to log: `0` is local, `3` is east-west pod-to-pod. | Keep both for full visibility. |
| `outputs.flow_log.throttles.l7_throttle` | `50000` | Maximum L7 records per second per agent. | Raise if you observe drops in DeepFlow Server logs. |

### 4.5 Verify DeepFlow Is Capturing Traffic

```bash
# Send a test request through one of your services
curl http://your-service.example.internal:8080/some/path

# Wait 30 seconds, then query the captured flows directly
kubectl exec -n deepflow deepflow-clickhouse-0 -- clickhouse-client \
    --query "SELECT request_type, endpoint, response_code, start_time
             FROM flow_log.l7_flow_log
             WHERE start_time > now() - INTERVAL 5 MINUTE
             ORDER BY start_time DESC
             LIMIT 10"
```

If the test path appears in the result, DeepFlow is working.

---

## 5. Stage 2: Set Up the API Discovery Server

### 5.1 Choose a Deployment Path

| Path | When to use |
|---|---|
| **Helm** | Production Kubernetes. Provides bundled PostgreSQL by default with a clean opt-out for managed databases. |
| **Docker Compose** | Single-host environments, demos, lab setups. |
| **VM Installer** | Bare-metal or virtual-machine hosts where Docker and Kubernetes are not available. |

All three paths use the same `config.toml` file. The differences are only in how the daemon process and PostgreSQL are provisioned.

### 5.2 Helm Deployment

```bash
# Pull the Bitnami Postgres subchart dependency (one-time)
helm repo add bitnami https://charts.bitnami.com/bitnami
helm dependency update deploy/helm/ads

# Install with bundled PostgreSQL (default)
helm install discovery deploy/helm/ads \
    -n governance --create-namespace

# Verify pods reach Running
kubectl get pods -n governance --watch
```

**To use an existing managed PostgreSQL** instead of the bundled one:

```bash
# Create a Kubernetes Secret with your DB password (key must be "password")
kubectl create secret generic ads-db-credentials \
    -n governance \
    --from-literal=password='your-db-password'

# Install pointing at the external database
helm install discovery deploy/helm/ads \
    -n governance --create-namespace \
    -f deploy/helm/ads/values-external.yaml \
    --set database.host=postgres.prod.svc.cluster.local \
    --set database.passwordSecret=ads-db-credentials
```

### 5.3 Docker Compose Deployment

```bash
# Copy the example env and config files
cp deploy/docker/.env.example deploy/docker/.env
cp deploy/docker/config.toml.example deploy/docker/config.toml

# Edit .env. Set:
#   ADS_DB_PASSWORD             (any strong password)
#   APIM_SVC_PASSWORD           (your WSO2 API Manager service account password)
#   APIM_INTROSPECT_BASIC_AUTH  (base64 of client_id:client_secret)
${EDITOR:-nano} deploy/docker/.env

# Edit config.toml. Set:
#   [deepflow] clickhouse_url   to your DeepFlow Querier URL
#   [apim]     publisher_url    to your WSO2 API Manager URL
#   [apim]     introspect_url   to your WSO2 API Manager introspect URL
${EDITOR:-nano} deploy/docker/config.toml

# Bring the stack up
docker compose -f deploy/docker/docker-compose.yml up -d

# Verify both containers reach healthy state
docker compose -f deploy/docker/docker-compose.yml ps
```

### 5.4 VM Installer Deployment

```bash
# Build the binary (requires Go 1.22+)
make build

# Run the installer (idempotent, safe to re-run)
sudo deploy/install/install.sh

# Watch the daemon logs
sudo journalctl -u ads.service -f
```

**To use an external PostgreSQL:**

```bash
sudo deploy/install/install.sh \
    --external-db DSN=postgres://ads:secret@postgres.example.internal:5432/ads
```

**To uninstall (preserves the database):**

```bash
sudo deploy/install/install.sh --uninstall
```

### 5.5 Configuration File Reference

The configuration file is `config.toml`. All deployment paths use the same file. The full structure is shown below; field meanings follow in the tables for each section.

```toml
[ads]
name      = "wso2-api-discovery-server"
version   = "1.0.0"
log_level = "info"

[database]
host                    = "localhost"
port                    = 5432
name                    = "ads"
user                    = "ads"
password                = "${ADS_DB_PASSWORD}"
sslmode                 = "disable"
max_open_conns          = 25
max_idle_conns          = 5
connect_timeout_seconds = 10

[deepflow]
enabled             = true
clickhouse_url      = "http://deepflow-server.deepflow.svc.cluster.local:30617"
clickhouse_user     = "default"
clickhouse_password = "${DEEPFLOW_CH_PASSWORD:-}"
verify_ssl          = false
timeout_seconds     = 30

[apim]
publisher_url            = "https://apim.example.internal:9443"
service_account_username = "ads-service"
service_account_password = "${APIM_SVC_PASSWORD}"
verify_ssl               = false
timeout_seconds          = 30
introspect_url           = "https://apim.example.internal:9443/oauth2/introspect"
introspect_basic_auth    = "${APIM_INTROSPECT_BASIC_AUTH}"

[discovery]
poll_interval_minutes     = 5
window_minutes            = 5
status_min                = 200
status_max                = 400
skip_internal             = false
min_observations          = 1
max_signatures_per_window = 10000

[discovery.noise_filter]
path_patterns = [
    "/health", "/healthz", "/ping", "/ready", "/readiness",
    "/liveness", "/actuator",
    "/agent.Synchronizer", "/grpc.health.v1.HealthService",
    "/controller.", "/tagrecorder.",
    "/metadata", "/machine", "/vmSettings", "/vmAgentLog",
    "/latest/meta-data", "/computeMetadata",
    "/apis/", "/api/v1/",
    "/favicon.ico", "/.well-known", "/swagger", "/openapi",
    "/robots.txt", "/.env",
]
path_exact       = ["/", "/version", "/metrics"]
excluded_ports   = []
excluded_domains = ["169.254.169.254"]

[discovery.normalization]
version_pattern = "^v?[0-9]+\\.[0-9]+(\\.[0-9]+)?$"
builtin_patterns = [
    "^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$",
    "^[0-9a-fA-F]{24,}$",
    "^[0-9]{4}-[0-9]{2}-[0-9]{2}$",
    "^[0-9]+$",
    "^[A-Za-z0-9_-]{20,}={0,2}$",
    "^[A-Z]{2,5}-[A-Z0-9-]{3,}$",
]
user_patterns = []
exclude_patterns = [
    "^v?[0-9]+\\.[0-9]+(\\.[0-9]+)?$",
    "^v[0-9]+$",
    "^api$",
]

[managed]
poll_interval_minutes = 10
fetch_concurrency     = 5

[comparison]
freshness_threshold_multiplier = 3

[bff]
listen_addr           = "0.0.0.0:8443"
tls_cert              = "/etc/ads/certs/server.crt"
tls_key               = "/etc/ads/certs/server.key"
verify_client_cert    = false
read_timeout_seconds  = 30
write_timeout_seconds = 30

[bff.token_cache]
ttl_seconds = 30
max_entries = 1000

[health]
listen_addr = "0.0.0.0:9090"

[k8s]
enabled = false

[retention]
classifications_retention_days = 90
discovered_apis_retention_days = 30
```

#### `[ads]` general settings

| Field | Default | Purpose |
|---|---|---|
| `name` | `wso2-api-discovery-server` | Logical instance name, used in logs. |
| `version` | `1.0.0` | Version string surfaced in logs and health response. |
| `log_level` | `info` | One of `debug`, `info`, `warn`, `error`. Use `info` in production. |

#### `[database]` PostgreSQL connection

| Field | Default | Purpose |
|---|---|---|
| `host`, `port`, `name`, `user` | localhost / 5432 / ads / ads | Connection parameters. |
| `password` | `${ADS_DB_PASSWORD}` | Read from environment via `${VAR}` substitution. Never put a literal password here. |
| `sslmode` | `disable` | `disable`, `require`, `verify-ca`, or `verify-full`. Use `require` or stronger against any external database. |
| `max_open_conns` | `25` | Maximum simultaneous connections. |
| `max_idle_conns` | `5` | Maximum idle connections in the pool. |
| `connect_timeout_seconds` | `10` | Seconds to wait for the initial connection. |

#### `[deepflow]` Querier connection

| Field | Default | Purpose |
|---|---|---|
| `enabled` | `true` | Master switch for the discovery cycle. |
| `clickhouse_url` | empty | DeepFlow Querier HTTP endpoint. Note: the Querier, not direct ClickHouse. |
| `clickhouse_user` | `default` | Querier username. |
| `clickhouse_password` | empty | Querier password if set. |
| `verify_ssl` | `false` | Whether to verify TLS. |
| `timeout_seconds` | `30` | HTTP timeout per Querier request. |

#### `[apim]` WSO2 API Manager connection

| Field | Default | Purpose |
|---|---|---|
| `publisher_url` | empty | Base URL of the WSO2 API Manager Publisher REST API. |
| `service_account_username`, `service_account_password` | empty | Credentials for a dedicated service account in WSO2 API Manager. Do not reuse a human user. |
| `verify_ssl` | `true` | Whether to verify the WSO2 API Manager TLS certificate. |
| `timeout_seconds` | `30` | HTTP timeout per WSO2 API Manager request. |
| `introspect_url` | empty | OAuth2 token introspection endpoint, used by the BFF for token verification. |
| `introspect_basic_auth` | empty | Base64 of `client_id:client_secret` from Dynamic Client Registration. |

#### `[discovery]` discovery cycle

| Field | Default | Purpose |
|---|---|---|
| `poll_interval_minutes` | `5` | How often the discovery cycle runs. |
| `window_minutes` | `5` | Time window each cycle queries. Must equal `poll_interval_minutes`. |
| `status_min` / `status_max` | `200` / `400` | HTTP status range to consider. Default ignores 4xx and 5xx errors. |
| `skip_internal` | `false` | Drop traffic that travelled only inside the cluster. Recommended `false`. |
| `min_observations` | `1` | Minimum observations before recording a path. Raise to 3 or 5 in noisy environments. |
| `max_signatures_per_window` | `10000` | Hard cap on rows pulled per cycle. |

#### `[discovery.noise_filter]` noise filter

| Field | Default | Purpose |
|---|---|---|
| `path_patterns` | health, readiness, metadata, swagger, etc. | **Substring contains** match. Drops any path that contains the entry. |
| `path_exact` | `["/", "/version", "/metrics"]` | **Exact equality** match. Drops only paths that exactly equal an entry. |
| `excluded_ports` | `[]` | Server ports to exclude. |
| `excluded_domains` | `["169.254.169.254"]` | Request domains to exclude. |

To extend, add entries to either list. Use `path_patterns` for paths that appear under different version prefixes; use `path_exact` for short paths where substring matching would over-match (such as `/`).

#### `[discovery.normalization]` path normalisation

The normaliser collapses dynamic segments to `{id}`. For example `/customers/abc-123` becomes `/customers/{id}` so they aggregate into the same finding. Each segment is checked independently against three pattern lists.

| Field | Purpose |
|---|---|
| `exclude_patterns` | Patterns that prevent normalisation. Used to preserve version segments. |
| `builtin_patterns` | Standard dynamic-identifier patterns shipped with the product. |
| `user_patterns` | Empty by default. Add your enterprise-specific identifier patterns here. |

**Currently supported builtin patterns:**

| Regex | Matches |
|---|---|
| `^[0-9a-fA-F]{8}-...{12}$` | UUID v4 (e.g. `c8293ec0-1234-4abc-8def-1234567890ab`) |
| `^[0-9a-fA-F]{24,}$` | MongoDB ObjectID, hex hashes |
| `^[0-9]{4}-[0-9]{2}-[0-9]{2}$` | ISO date (e.g. `2026-04-30`) |
| `^[0-9]+$` | Numeric ID |
| `^[A-Za-z0-9_-]{20,}={0,2}$` | Base64-like opaque token |
| `^[A-Z]{2,5}-[A-Z0-9-]{3,}$` | SKU pattern (e.g. `CUST-001`, `SKU-IPHONE-15`) |

**Currently supported exclude patterns:**

| Regex | Reason excluded |
|---|---|
| `^v?[0-9]+\.[0-9]+(\.[0-9]+)?$` | Semantic versions like `1.0.0`, `v1.2.3` |
| `^v[0-9]+$` | Short versions like `v1`, `v2` |
| `^api$` | The literal word `api` |

**To add your own pattern**, append to `user_patterns`. Patterns must be anchored with `^...$` and use Go's RE2 syntax (no backreferences or lookahead).

```toml
[discovery.normalization]
user_patterns = [
    "^CST_[0-9]{8}$",          # Customer IDs like CST_00123456
    "^ORD-[0-9A-F]{16}$",      # Order IDs (16 hex characters)
    "^TKN-[A-Za-z0-9]{32}$",   # API keys
]
```

Restart the daemon after editing patterns.

#### `[managed]` managed-API sync cycle

| Field | Default | Purpose |
|---|---|---|
| `poll_interval_minutes` | `10` | How often to refresh managed APIs from WSO2 API Manager. |
| `fetch_concurrency` | `5` | Parallel WSO2 API Manager calls when fetching API details. |

#### `[comparison]`

| Field | Default | Purpose |
|---|---|---|
| `freshness_threshold_multiplier` | `3` | Comparison runs only if managed sync succeeded within `multiplier × managed.poll_interval` minutes. Prevents false Shadow classifications when managed data is stale. |

#### `[bff]` REST surface

| Field | Default | Purpose |
|---|---|---|
| `listen_addr` | `0.0.0.0:8443` | Address and port the BFF listens on. |
| `tls_cert`, `tls_key` | paths | TLS certificate and key. The bundled installers auto-generate these. |
| `verify_client_cert` | `false` | Whether to require client certificates (mTLS). Authentication uses bearer-token introspection. |
| `read_timeout_seconds`, `write_timeout_seconds` | `30` | HTTP timeouts. |

#### `[bff.token_cache]`

| Field | Default | Purpose |
|---|---|---|
| `ttl_seconds` | `30` | How long an introspected token result is cached. |
| `max_entries` | `1000` | Maximum cached tokens. |

#### `[health]` health probes

| Field | Default | Purpose |
|---|---|---|
| `listen_addr` | `0.0.0.0:9090` | Address for `/healthz` and `/readyz`. |

#### `[k8s]`

| Field | Default | Purpose |
|---|---|---|
| `enabled` | `false` | Enables Kubernetes leader election for multi-replica deployments. |

#### `[retention]`

| Field | Default | Purpose |
|---|---|---|
| `classifications_retention_days` | `90` | How long to keep historical classification rows. |
| `discovered_apis_retention_days` | `30` | How long to keep idle discovered API rows. Anchor rows are exempt. |

### 5.6 Verify the Daemon Is Healthy

```bash
# Liveness check (always 200 if the process is running)
curl http://<daemon-host>:9090/healthz

# Readiness check (200 only if all internal cycles are healthy)
curl http://<daemon-host>:9090/readyz
```

Expected output of `/healthz`:

```json
{"status":"ok"}
```

---

## 6. Stage 3: Set Up WSO2 API Manager

### 6.1 Add the deployment.toml Block

Edit `<APIM_HOME>/repository/conf/deployment.toml` and append the block below.

```toml
[apim.governance.discovery]
enabled    = true
endpoint   = "https://your-discovery-server-host:8443"
timeout_ms = 5000
verify_ssl = false
```

| Field | Purpose |
|---|---|
| `enabled` | Master switch. When `false`, the Unmanaged APIs tab is hidden in the Admin Portal. |
| `endpoint` | Base URL where WSO2 API Manager can reach the API Discovery Server. |
| `timeout_ms` | HTTP timeout per call from WSO2 API Manager to the daemon. |
| `verify_ssl` | Whether to verify the daemon's TLS certificate. Set `false` while using a self-signed certificate. |

### 6.2 Restart WSO2 API Manager

```bash
# Stop
<APIM_HOME>/bin/api-manager.sh stop

# Wait until the process exits
sleep 10

# Start
<APIM_HOME>/bin/api-manager.sh

# Tail the startup log
tail -f <APIM_HOME>/repository/logs/wso2carbon.log | grep -E "Carbon started|APIDiscovery"
```

### 6.3 Register the Scope in Carbon Console

The feature uses the OAuth2 scope `apim:admin_discovery_view`. Register it once via the Carbon Management Console.

1. Open `https://<apim-host>:9443/carbon/`
2. Log in as admin
3. Navigate to **Configure > Roles**
4. Click **Edit Permissions** on the `admin` role
5. Find the OAuth2 Scopes section and add a new scope:
    * **Name:** `apim:admin_discovery_view`
    * **Description:** `View discovered unmanaged APIs (Governance)`
    * **Roles:** `admin`
6. Save

### 6.4 Verify the Tab Appears

1. Open the Admin Portal at `https://<apim-host>:9443/admin/`
2. Log in as admin
3. The left navigation should now show **Governance > Unmanaged APIs**

You can also verify the feature flag programmatically:

```bash
# Get an admin token
TOKEN=$(curl -sk -u "<client_id>:<client_secret>" \
    -d "grant_type=password&username=admin&password=admin&scope=apim:admin" \
    https://<apim-host>:9443/oauth2/token | jq -r .access_token)

# Check the settings endpoint
curl -sk -H "Authorization: Bearer $TOKEN" \
    https://<apim-host>:9443/api/am/admin/v4/settings | jq .discoveryEnabled
```

Expected output: `true`.

---

## 7. Run the System and View Results

### 7.1 Generate Test Traffic

Send HTTP requests to one or more services. Hit at least one path that is NOT declared in WSO2 API Manager so the system has something to flag.

```bash
# Hit an undeclared path on a managed service (will become a Drift finding)
curl http://customers.example.internal:8084/customers/1.0.0/debug/dump

# Hit a service that has no managed APIs at all (will become a Shadow finding)
curl http://warehouse.example.internal:8085/warehouse/1.0.0/stock/SKU-12345

# Repeat several times so observation_count > 1
for i in $(seq 1 5); do
    curl http://customers.example.internal:8084/customers/1.0.0/debug/dump
done
```

### 7.2 Wait for the Discovery Cycle

The discovery cycle runs every 5 minutes. Wait for at least one full cycle to complete.

```bash
# VM (systemd)
sudo journalctl -u ads.service -f | grep "phase 1 cycle complete"

# Kubernetes
kubectl logs -n governance -l app.kubernetes.io/name=ads -f | grep "phase 1 cycle complete"

# Docker Compose
docker compose -f deploy/docker/docker-compose.yml logs -f ads | grep "phase 1 cycle complete"
```

### 7.3 View Findings in the Admin Portal

1. Open the Admin Portal: `https://<apim-host>:9443/admin/`
2. Navigate to **Governance > Unmanaged APIs**
3. Expected view:
    * Two donut charts at the top: Managed vs Unmanaged, and Shadow vs Drift
    * A findings table listing your test paths with their classifications
    * Click any row for the detail view: metadata table, top callers, related managed APIs

### 7.4 View Findings via the REST API

```bash
# Get an admin token (with the discovery scope)
TOKEN=$(curl -sk -u "<client_id>:<client_secret>" \
    -d "grant_type=password&username=admin&password=admin&scope=apim:admin apim:admin_discovery_view" \
    https://<apim-host>:9443/oauth2/token | jq -r .access_token)

# Aggregate summary
curl -sk -H "Authorization: Bearer $TOKEN" \
    https://<apim-host>:9443/api/am/admin/v4/governance/discovery/summary | jq

# Findings list
curl -sk -H "Authorization: Bearer $TOKEN" \
    https://<apim-host>:9443/api/am/admin/v4/governance/discovery/apis | jq

# Detail of a specific finding
FINDING_ID="<paste-an-id-from-the-list>"
curl -sk -H "Authorization: Bearer $TOKEN" \
    https://<apim-host>:9443/api/am/admin/v4/governance/discovery/apis/$FINDING_ID | jq
```

### 7.5 View Findings via Direct Database Queries

Connect to the API Discovery Server's PostgreSQL for a deeper look.

```bash
# VM (bundled Postgres)
sudo -u postgres psql -d ads

# Kubernetes (bundled Postgres subchart)
kubectl exec -it -n governance discovery-postgresql-0 -- psql -U ads -d ads

# Docker Compose
docker compose -f deploy/docker/docker-compose.yml exec postgres psql -U ads -d ads
```

```sql
-- Count findings by classification
SELECT classification, COUNT(*)
FROM v_current_classifications
GROUP BY classification;

-- List all current findings
SELECT method, normalized_path, service_identity, classification
FROM v_current_classifications
ORDER BY last_seen_at DESC;

-- Inspect top callers for a specific finding
SELECT method, normalized_path, top_clients
FROM ads_discovered_apis
WHERE normalized_path = '/customers/1.0.0/debug/dump';

-- Confirm the managed API sync has populated rows
SELECT COUNT(*) FROM ads_managed_apis WHERE is_active = true;
```

---

## 8. Viewing Logs

### 8.1 API Discovery Server Logs

| Deployment | Command |
|---|---|
| **Helm / Kubernetes** | `kubectl logs -n governance -l app.kubernetes.io/name=ads -f` |
| **Docker Compose** | `docker compose -f deploy/docker/docker-compose.yml logs -f ads` |
| **VM (systemd)** | `sudo journalctl -u ads.service -f` |

The log is structured JSON. To follow only cycle results:

```bash
# Helm / Kubernetes
kubectl logs -n governance -l app.kubernetes.io/name=ads -f \
    | grep -E "phase 1 cycle complete|phase 2 cycle complete|phase 3 cycle complete"

# VM
sudo journalctl -u ads.service -f \
    | grep -E "phase 1 cycle complete|phase 2 cycle complete|phase 3 cycle complete"
```

### 8.2 WSO2 API Manager Logs

```bash
# Full log
tail -f <APIM_HOME>/repository/logs/wso2carbon.log

# Discovery-related entries only
tail -f <APIM_HOME>/repository/logs/wso2carbon.log \
    | grep -E "DiscoveryApiServer|GovernanceApi"
```

### 8.3 DeepFlow Logs

```bash
# Server logs
kubectl logs -n deepflow deepflow-server-0 -f

# Agent logs (per node)
kubectl logs -n deepflow -l app=deepflow-agent --tail=100
```

---

## 9. Troubleshooting

### 9.1 The Unmanaged APIs Tab Does Not Appear

Check, in order:

```bash
# 1. The toml flag is set
grep -A4 "apim.governance.discovery" <APIM_HOME>/repository/conf/deployment.toml

# 2. The XML was regenerated from the toml
grep -A4 "<APIDiscovery>" <APIM_HOME>/repository/conf/api-manager.xml

# 3. The settings endpoint reports the flag
TOKEN=$(curl -sk -u "<client_id>:<client_secret>" \
    -d "grant_type=password&username=admin&password=admin&scope=apim:admin" \
    https://<apim-host>:9443/oauth2/token | jq -r .access_token)

curl -sk -H "Authorization: Bearer $TOKEN" \
    https://<apim-host>:9443/api/am/admin/v4/settings | jq .discoveryEnabled
```

If `discoveryEnabled` is `false`, the toml block was not picked up. Delete the metadata files under `<APIM_HOME>/repository/resources/conf/.metadata/` and restart WSO2 API Manager to force regeneration. Hard-refresh your browser (Ctrl+Shift+R) to clear cached HTML.

### 9.2 The Tab Appears But the List Is Empty

```bash
# 1. Daemon health
curl http://<daemon-host>:9090/readyz

# 2. Daemon errors
sudo journalctl -u ads.service -n 100 | grep -E "ERROR|cycle failed"

# 3. Confirm DeepFlow has data
kubectl exec -n deepflow deepflow-clickhouse-0 -- clickhouse-client \
    --query "SELECT count() FROM flow_log.l7_flow_log WHERE start_time > now() - INTERVAL 5 MINUTE"

# 4. Check the daemon's database
sudo -u postgres psql -d ads -c "SELECT COUNT(*) FROM ads_discovered_apis WHERE is_active = true;"
```

If discovered rows exist but no classifications, force a manual comparison cycle by restarting the daemon, or wait for the next 5-minute cycle.

### 9.3 Findings Show the Wrong Classification

* **Shadow** means no managed API exists on the same service. If you expected Drift, register at least one managed API for that service in WSO2 API Manager.
* **Drift** means at least one managed API exists on the same service and the discovered path is a deviation from it.

The matching key is `(method, path)` where `path` is the normalised path. Check that the managed API's gateway path uses the same parameter convention (`{id}`) as the discovered path.

### 9.4 The Top Callers List Is Empty

The DeepFlow agent did not classify the source of the traffic. Two common causes:

* **The caller is outside the cluster's tracked domain.** DeepFlow tags such sources as internet/unknown. The daemon falls back to recording the bare IP address, so the list should still populate.
* **The traffic was captured at a server-side observation point that did not include the source IP.** Ensure the agent's `outputs.flow_log.filters.l7_capture_network_types` includes both `0` (local) and `3` (east-west).

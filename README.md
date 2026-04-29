# WSO2 API Discovery Server (ADS)

Discovers unmanaged APIs in runtime traffic, compares them against APIs registered in WSO2 API Manager, and surfaces the gaps inside the APIM Admin Portal as a new "Unmanaged APIs" tab under the Governance section.

## Architecture

Three deployable components:

1. **ADS daemon** (this repo) — Go service that observes traffic via DeepFlow and reconciles against APIM.
2. **carbon-apimgt fork** — adds a Backend-for-Frontend resource group under `/api/am/admin/v4/governance/discovery/*`.
3. **apim-apps fork** — adds the "Unmanaged APIs" tab in the admin portal Governance section.

## Install — pick one

ADS ships with a **bundled Postgres by default** for one-shot installs. Operators with an existing managed Postgres can switch to external mode in any of the three install paths.

### A. Kubernetes (Helm)

```bash
helm repo add bitnami https://charts.bitnami.com/bitnami
helm dependency update deploy/helm/ads

# Bundled (default) — provisions Postgres alongside ADS
helm install ads deploy/helm/ads -n governance --create-namespace

# External — point at your existing Postgres
helm install ads deploy/helm/ads -n governance --create-namespace \
    -f deploy/helm/ads/values-external.yaml \
    --set database.host=postgres.prod.svc.cluster.local \
    --set database.passwordSecret=ads-db-credentials
```

The bundled subchart is `bitnami/postgresql` (Postgres 16, single replica, 10Gi PVC, ClusterIP-only). Override sizing in `values.yaml` under `postgresql.primary.persistence` and `postgresql.primary.resources`.

### B. Docker Compose

```bash
cp deploy/docker/.env.example deploy/docker/.env
cp deploy/docker/config.toml.example deploy/docker/config.toml
# edit .env (passwords, APIM creds) and config.toml (DeepFlow + APIM URLs)

# Bundled (default)
docker compose -f deploy/docker/docker-compose.yml up -d

# External Postgres
docker compose \
    -f deploy/docker/docker-compose.yml \
    -f deploy/docker/docker-compose.external.yml \
    up -d
```

### C. VM (systemd)

```bash
make build
sudo deploy/install/install.sh                 # bundled Postgres on this host
# or:
sudo deploy/install/install.sh \
    --external-db DSN=postgres://ads:secret@postgres.example.internal:5432/ads
```

The installer is idempotent: it creates the `ads` system user, installs Postgres 16 (bundled mode), generates a random DB password into `/etc/ads/secrets.env`, renders `/etc/ads/config.toml`, and starts `ads.service`. Runs against Ubuntu 22.04 / 24.04 and Debian 12.

## Local development

```bash
make build
./bin/ads --validate --config config/config.toml.example
./bin/ads --config config/config.toml.example
```

## Requirements

- Go 1.22+ (build only)
- DeepFlow 6.4+ for traffic observation
- WSO2 APIM 4.6.0+ (JDK 21 runtime)
- Postgres 13+ — bundled automatically by all three install paths above; or BYO

## License

Apache 2.0.

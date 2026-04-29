#!/usr/bin/env bash
# WSO2 API Discovery Server — VM installer (bundled Postgres by default).
#
# Tested on: Ubuntu 22.04 / 24.04, Debian 12.
# Run as root (sudo). The script is idempotent: re-running it after a
# partial failure picks up where it left off without duplicating users,
# databases, or systemd units.
#
# Usage:
#
#   sudo ./install.sh                              # bundled Postgres
#   sudo ./install.sh --external-db DSN=postgres://user:pass@host:5432/ads
#                                                  # use existing Postgres
#   sudo ./install.sh --uninstall                  # remove ADS (keeps DB data)
#
# What it does (bundled mode):
#   1. Installs postgresql-16 from apt
#   2. Creates ads user + ads database, generates a random password
#   3. Locks Postgres to listen on 127.0.0.1 only
#   4. Installs the ads binary to /usr/local/bin/ads
#   5. Renders /etc/ads/config.toml from the example
#   6. Writes /etc/ads/secrets.env (mode 0600) with the generated password
#   7. Installs and starts ads.service
#   8. Verifies /healthz comes up green within 60 seconds

set -euo pipefail

# ---- Defaults (override via env or flags) -----------------------------------
ADS_USER="${ADS_USER:-ads}"
ADS_GROUP="${ADS_GROUP:-ads}"
ADS_BIN="${ADS_BIN:-/usr/local/bin/ads}"
ADS_CONFIG_DIR="${ADS_CONFIG_DIR:-/etc/ads}"
ADS_LOG_DIR="${ADS_LOG_DIR:-/var/log/ads}"
ADS_LIB_DIR="${ADS_LIB_DIR:-/var/lib/ads}"
ADS_DB_NAME="${ADS_DB_NAME:-ads}"
ADS_DB_USER="${ADS_DB_USER:-ads}"
PG_VERSION="${PG_VERSION:-16}"

EXTERNAL_DSN=""
UNINSTALL=0
SOURCE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# ---- Helpers ---------------------------------------------------------------
log()  { printf '[install] %s\n' "$*"; }
warn() { printf '[install] WARN: %s\n' "$*" >&2; }
die()  { printf '[install] ERROR: %s\n' "$*" >&2; exit 1; }

require_root() {
    [[ $EUID -eq 0 ]] || die "must run as root (try: sudo $0 $*)"
}

usage() {
    sed -n '2,/^set -euo/p' "$0" | sed -n '/^#/p' | sed 's/^# \{0,1\}//'
    exit 0
}

# ---- Arg parsing -----------------------------------------------------------
while [[ $# -gt 0 ]]; do
    case "$1" in
        --external-db) shift; EXTERNAL_DSN="${1#DSN=}"; shift ;;
        --uninstall)   UNINSTALL=1; shift ;;
        -h|--help)     usage ;;
        *)             die "unknown flag: $1 (try --help)" ;;
    esac
done

require_root "$@"

# ---- Uninstall path --------------------------------------------------------
if [[ $UNINSTALL -eq 1 ]]; then
    log "stopping ads.service"
    systemctl disable --now ads.service 2>/dev/null || true
    rm -f /etc/systemd/system/ads.service "$ADS_BIN"
    rm -rf "$ADS_CONFIG_DIR" "$ADS_LOG_DIR"
    systemctl daemon-reload
    log "uninstalled. Postgres data in $ADS_LIB_DIR and database '$ADS_DB_NAME' were left in place."
    exit 0
fi

# ---- Install path ----------------------------------------------------------
log "WSO2 API Discovery Server installer"
log "source directory: $SOURCE_DIR"
[[ -n $EXTERNAL_DSN ]] && log "mode: external Postgres" || log "mode: bundled Postgres"

# 1. System user / dirs
if ! id "$ADS_USER" &>/dev/null; then
    log "creating system user '$ADS_USER'"
    useradd -r -s /usr/sbin/nologin -d /var/lib/ads "$ADS_USER"
fi
install -d -o "$ADS_USER" -g "$ADS_GROUP" "$ADS_LOG_DIR" "$ADS_LIB_DIR"
install -d -m 0755 "$ADS_CONFIG_DIR"
install -d -m 0750 -o root -g "$ADS_GROUP" "$ADS_CONFIG_DIR/certs"

# 1a. TLS certificates for the BFF endpoint.
#     Auto-generate a self-signed cert + key pair if neither exists. Skips
#     re-generation on re-runs so operator-supplied certs are preserved.
#     Uses the host's FQDN as CN with 825-day validity (Apple's max for
#     leaf certs; other clients accept it cleanly).
ADS_CRT="$ADS_CONFIG_DIR/certs/server.crt"
ADS_KEY="$ADS_CONFIG_DIR/certs/server.key"
if [[ ! -f $ADS_CRT || ! -f $ADS_KEY ]]; then
    if ! command -v openssl >/dev/null 2>&1; then
        export DEBIAN_FRONTEND=noninteractive
        apt-get update -qq
        apt-get install -y -qq openssl
    fi
    CN="$(hostname -f 2>/dev/null || hostname)"
    log "generating self-signed TLS cert (CN=$CN, 825 days)"
    openssl req -x509 -newkey rsa:2048 -nodes \
        -keyout "$ADS_KEY" -out "$ADS_CRT" \
        -days 825 -subj "/CN=$CN" \
        -addext "subjectAltName=DNS:$CN,DNS:localhost,IP:127.0.0.1" \
        2>/dev/null
    chown root:"$ADS_GROUP" "$ADS_CRT" "$ADS_KEY"
    chmod 0644 "$ADS_CRT"
    chmod 0640 "$ADS_KEY"
else
    log "TLS certs already present at $ADS_CONFIG_DIR/certs/, leaving in place"
fi

# 2. Bundled Postgres
if [[ -z $EXTERNAL_DSN ]]; then
    if ! command -v psql >/dev/null 2>&1; then
        log "installing postgresql-${PG_VERSION}"
        export DEBIAN_FRONTEND=noninteractive
        apt-get update -qq
        apt-get install -y -qq "postgresql-${PG_VERSION}" "postgresql-client-${PG_VERSION}" openssl
    fi

    PG_HBA="/etc/postgresql/${PG_VERSION}/main/pg_hba.conf"
    PG_CONF="/etc/postgresql/${PG_VERSION}/main/postgresql.conf"
    if [[ -f $PG_CONF ]] && ! grep -q "^listen_addresses *= *'localhost'" "$PG_CONF"; then
        log "locking Postgres to listen on localhost only"
        sed -i "s/^#\?listen_addresses *=.*/listen_addresses = 'localhost'/" "$PG_CONF"
        systemctl restart "postgresql@${PG_VERSION}-main" || systemctl restart postgresql
    fi

    # Random password if not already provisioned
    if [[ ! -f "$ADS_CONFIG_DIR/secrets.env" ]] || ! grep -q "^ADS_DB_PASSWORD=" "$ADS_CONFIG_DIR/secrets.env"; then
        ADS_DB_PASSWORD="$(openssl rand -hex 32)"
        log "generated new Postgres password (256-bit)"
    else
        ADS_DB_PASSWORD="$(grep '^ADS_DB_PASSWORD=' "$ADS_CONFIG_DIR/secrets.env" | cut -d= -f2-)"
        log "reusing existing password from secrets.env"
    fi

    # Create role + database (idempotent)
    if ! sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='${ADS_DB_USER}'" | grep -q 1; then
        log "creating Postgres role '${ADS_DB_USER}'"
        sudo -u postgres psql -c "CREATE ROLE \"${ADS_DB_USER}\" LOGIN PASSWORD '${ADS_DB_PASSWORD}'"
    else
        log "updating password on existing role '${ADS_DB_USER}'"
        sudo -u postgres psql -c "ALTER ROLE \"${ADS_DB_USER}\" WITH PASSWORD '${ADS_DB_PASSWORD}'"
    fi
    if ! sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='${ADS_DB_NAME}'" | grep -q 1; then
        log "creating Postgres database '${ADS_DB_NAME}'"
        sudo -u postgres psql -c "CREATE DATABASE \"${ADS_DB_NAME}\" OWNER \"${ADS_DB_USER}\""
    fi

    DB_HOST="localhost"
    DB_PORT="5432"
    DB_SSLMODE="disable"
else
    # Parse DSN: postgres://user:pass@host:port/db
    if [[ ! $EXTERNAL_DSN =~ ^postgres://([^:]+):([^@]+)@([^:]+):([0-9]+)/(.+)$ ]]; then
        die "external DSN must be: postgres://user:password@host:port/database"
    fi
    ADS_DB_USER="${BASH_REMATCH[1]}"
    ADS_DB_PASSWORD="${BASH_REMATCH[2]}"
    DB_HOST="${BASH_REMATCH[3]}"
    DB_PORT="${BASH_REMATCH[4]}"
    ADS_DB_NAME="${BASH_REMATCH[5]}"
    DB_SSLMODE="require"
    log "external Postgres: ${ADS_DB_USER}@${DB_HOST}:${DB_PORT}/${ADS_DB_NAME}"
fi

# 3. Binary
if [[ -x "$SOURCE_DIR/bin/ads" ]]; then
    log "installing binary from $SOURCE_DIR/bin/ads"
    install -m 0755 "$SOURCE_DIR/bin/ads" "$ADS_BIN"
elif [[ -x "$ADS_BIN" ]]; then
    log "binary already at $ADS_BIN, leaving in place"
else
    die "no binary found at $SOURCE_DIR/bin/ads — run 'make build' first, or place ads at $ADS_BIN"
fi

# 4. Config — render from example with DB host/port substituted
EXAMPLE_CFG="$SOURCE_DIR/config/config.toml.example"
[[ -f $EXAMPLE_CFG ]] || die "missing $EXAMPLE_CFG — run from a checked-out repo"
if [[ ! -f "$ADS_CONFIG_DIR/config.toml" ]]; then
    log "rendering $ADS_CONFIG_DIR/config.toml from example"
    sed \
        -e "s|^host *=.*|host                    = \"${DB_HOST}\"|" \
        -e "s|^port *= *5432|port                    = ${DB_PORT}|" \
        -e "s|^sslmode *= *\"[^\"]*\"|sslmode                 = \"${DB_SSLMODE}\"|" \
        -e "s|^name *= *\"ads\"|name                    = \"${ADS_DB_NAME}\"|" \
        -e "s|^user *= *\"ads\"|user                    = \"${ADS_DB_USER}\"|" \
        "$EXAMPLE_CFG" > "$ADS_CONFIG_DIR/config.toml"
    chmod 0644 "$ADS_CONFIG_DIR/config.toml"
else
    log "config already at $ADS_CONFIG_DIR/config.toml, leaving in place"
fi

# 5. Secrets
SECRETS_FILE="$ADS_CONFIG_DIR/secrets.env"
if [[ ! -f $SECRETS_FILE ]]; then
    log "writing $SECRETS_FILE (mode 0600)"
    cat > "$SECRETS_FILE" <<EOF
# Generated by install.sh on $(date -Iseconds). Edit APIM_* values below.
ADS_DB_PASSWORD=${ADS_DB_PASSWORD}
APIM_SVC_PASSWORD=
APIM_INTROSPECT_BASIC_AUTH=
EOF
    chmod 0600 "$SECRETS_FILE"
    chown root:"$ADS_GROUP" "$SECRETS_FILE"
else
    # Update only the DB password line; leave APIM_* alone.
    log "updating ADS_DB_PASSWORD in existing $SECRETS_FILE"
    sed -i "s|^ADS_DB_PASSWORD=.*|ADS_DB_PASSWORD=${ADS_DB_PASSWORD}|" "$SECRETS_FILE"
fi

# 6. systemd unit
UNIT_SRC="$SOURCE_DIR/deploy/systemd/ads.service"
[[ -f $UNIT_SRC ]] || die "missing systemd unit at $UNIT_SRC"
if [[ ! -f /etc/systemd/system/ads.service ]] || \
   ! cmp -s "$UNIT_SRC" /etc/systemd/system/ads.service; then
    log "installing systemd unit"
    install -m 0644 "$UNIT_SRC" /etc/systemd/system/ads.service
    systemctl daemon-reload
fi

# 7. Start + verify
log "enabling and starting ads.service"
systemctl enable --now ads.service

log "waiting up to 60s for /healthz to come up"
for i in $(seq 1 30); do
    if curl -fsS http://127.0.0.1:9090/healthz >/dev/null 2>&1; then
        log "healthz green — ADS is up"
        log ""
        log "next steps:"
        log "  1. edit $SECRETS_FILE to populate APIM_SVC_PASSWORD and"
        log "     APIM_INTROSPECT_BASIC_AUTH, then: systemctl restart ads"
        log "  2. edit $ADS_CONFIG_DIR/config.toml to set deepflow + apim URLs"
        log "  3. tail logs:  journalctl -u ads -f"
        exit 0
    fi
    sleep 2
done

warn "/healthz did not come up within 60s — check 'journalctl -u ads -n 50'"
exit 1

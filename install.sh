#!/usr/bin/env bash
# Project Truffels — Automated Installation Script
# Installs and configures the full Docker-managed Bitcoin infrastructure stack.
#
# Prerequisites:
#   - Raspberry Pi 5 (8GB) running Raspberry Pi OS Lite 64-bit
#   - NVMe boot with ext4
#   - Memory cgroups enabled (cgroup_enable=memory in cmdline.txt)
#   - Internet connectivity
#
# Usage:
#   sudo ./install.sh [--skip-docker] [--skip-pull] [--restore-from /path/to/backup]
#
# This script is idempotent — safe to re-run.

set -euo pipefail

# --- Configuration -----------------------------------------------------------
TRUFFELS_BASE="/srv/truffels"
COMPOSE_DIR="$TRUFFELS_BASE/compose"
CONFIG_DIR="$TRUFFELS_BASE/config"
DATA_DIR="$TRUFFELS_BASE/data"
SECRETS_DIR="$TRUFFELS_BASE/secrets"

BITCOIN_IMAGE="btcpayserver/bitcoin:30.2@sha256:cff45bbc8e166bb3403675baea73cf597c7373f20a87a76101e3d849f766d61e"
ELECTRS_IMAGE="getumbrel/electrs:v0.11.0@sha256:0a2c6f573abfd8d724651c6ba1c1f3a9c740219c1cf0f4468043c3342170d8a5"
MEMPOOL_BACKEND_IMAGE="mempool/backend:v3.2.1@sha256:d3531090e3bdd9a3dd38151349c5027768c3b7132438db267df8d8f026e15e61"
MEMPOOL_FRONTEND_IMAGE="mempool/frontend:v3.2.1@sha256:dd126cf383bd425ad46710925697c6a7925675a535c1026c206f2c092231e106"
MARIADB_IMAGE="mariadb:lts@sha256:8164f184d16c30e2f159e30518113667b796306dff0fe558876ab1ff521a682f"
POSTGRES_IMAGE="postgres:16.13-alpine@sha256:20edbde7749f822887a1a022ad526fde0a47d6b2be9a8364433605cf65099416"
CADDY_IMAGE="caddy:2.11.2-alpine@sha256:fce4f15aad23222c0ac78a1220adf63bae7b94355d5ea28eee53910624acedfa"

SKIP_DOCKER=false
SKIP_PULL=false
RESTORE_PATH=""

# --- Parse arguments ----------------------------------------------------------
while [[ $# -gt 0 ]]; do
    case "$1" in
        --skip-docker) SKIP_DOCKER=true; shift ;;
        --skip-pull) SKIP_PULL=true; shift ;;
        --restore-from) RESTORE_PATH="$2"; shift 2 ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

# --- Helpers ------------------------------------------------------------------
log()  { echo -e "\n\033[1;32m[TRUFFELS]\033[0m $*"; }
warn() { echo -e "\n\033[1;33m[WARNING]\033[0m $*"; }
die()  { echo -e "\n\033[1;31m[ERROR]\033[0m $*" >&2; exit 1; }

require_root() {
    [[ $EUID -eq 0 ]] || die "This script must be run as root (use sudo)."
}

check_arch() {
    local arch
    arch=$(uname -m)
    [[ "$arch" == "aarch64" ]] || die "This script requires aarch64 (ARM64). Got: $arch"
}

check_cgroups() {
    local controllers
    controllers=$(cat /sys/fs/cgroup/cgroup.controllers 2>/dev/null || echo "")
    if ! echo "$controllers" | grep -q "memory"; then
        die "Memory cgroups not active. Add 'cgroup_enable=memory' to /boot/firmware/cmdline.txt and reboot."
    fi
    log "Memory cgroups: OK"
}

generate_password() {
    python3 -c "import os,base64; print(base64.urlsafe_b64encode(os.urandom(24)).decode().rstrip('='))"
}

generate_rpcauth() {
    local username="$1"
    python3 -c "
import hmac, hashlib, os
username = '$username'
salt = os.urandom(16).hex()
password = os.urandom(32).hex()
password_hmac = hmac.new(bytearray(salt, 'utf-8'), bytearray(password, 'utf-8'), 'SHA256').hexdigest()
print(f'RPCAUTH_LINE=rpcauth={username}:{salt}\${password_hmac}')
print(f'RPC_USER={username}')
print(f'RPC_PASSWORD={password}')
"
}

# --- Preflight ----------------------------------------------------------------
require_root
check_arch
check_cgroups

log "Starting Project Truffels installation..."

# --- Step 1: Docker -----------------------------------------------------------
if [[ "$SKIP_DOCKER" == false ]]; then
    if command -v docker &>/dev/null && docker version &>/dev/null; then
        log "Docker already installed: $(docker version --format '{{.Server.Version}}')"
    else
        log "Installing Docker Engine..."
        apt-get update -qq
        apt-get install -y -qq ca-certificates curl
        install -m 0755 -d /etc/apt/keyrings
        curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
        chmod a+r /etc/apt/keyrings/docker.asc

        tee /etc/apt/sources.list.d/docker.sources >/dev/null <<REPO
Types: deb
URIs: https://download.docker.com/linux/debian
Suites: $(. /etc/os-release && echo "$VERSION_CODENAME")
Components: stable
Signed-By: /etc/apt/keyrings/docker.asc
REPO

        apt-get update -qq
        apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
        log "Docker installed: $(docker version --format '{{.Server.Version}}')"
    fi

    # Docker daemon config
    if [[ ! -f /etc/docker/daemon.json ]] || ! grep -q '"live-restore"' /etc/docker/daemon.json; then
        log "Configuring Docker daemon..."
        install -d -m 0755 /etc/docker
        tee /etc/docker/daemon.json >/dev/null <<'DAEMON'
{
  "live-restore": true,
  "log-driver": "local",
  "log-opts": {
    "max-size": "10m",
    "max-file": "5"
  },
  "features": {
    "buildkit": true
  }
}
DAEMON
        systemctl restart docker
    fi
fi

# --- Step 2: Directory layout -------------------------------------------------
log "Creating directory layout..."
mkdir -p "$TRUFFELS_BASE"/{compose,config,data,logs,backups,secrets,tmp}
mkdir -p "$DATA_DIR"/{bitcoin/blockchain,ckpool/logs,electrs/db,mempool/mysql,ckstats/postgres,truffels}
mkdir -p "$CONFIG_DIR"/{bitcoin,electrs,ckpool,ckstats,proxy,nftables}
mkdir -p "$COMPOSE_DIR"/{bitcoin,electrs,ckpool,mempool,ckstats,proxy,truffels}
chmod 0755 "$TRUFFELS_BASE"
chmod 0750 "$SECRETS_DIR"
chown 1000:1000 "$DATA_DIR/truffels"

# --- Step 3: Data restore (optional) -----------------------------------------
if [[ -n "$RESTORE_PATH" ]]; then
    log "Restoring data from $RESTORE_PATH..."
    for dir in bitcoin ckpool electrs ckpoolstats; do
        if [[ -d "$RESTORE_PATH/$dir" ]]; then
            log "  Restoring $dir..."
            rsync -rltDh --info=progress2 "$RESTORE_PATH/$dir/" "$DATA_DIR/$dir/"
        fi
    done
fi

# Fix ownership — all data should be owned by uid 1000
chown -R 1000:1000 "$DATA_DIR"

# --- Step 4: Docker networks --------------------------------------------------
log "Creating Docker networks..."
docker network create --driver bridge --subnet 172.20.0.0/24 bitcoin-backend 2>/dev/null || true
docker network create --driver bridge --subnet 172.21.0.0/24 truffels-edge 2>/dev/null || true
docker network create --driver bridge --subnet 172.22.0.0/24 truffels-core 2>/dev/null || true

# --- Step 5: Generate credentials (if not already present) --------------------
if [[ ! -f "$SECRETS_DIR/rpc.env" ]]; then
    log "Generating RPC credentials..."
    eval "$(generate_rpcauth truffels)"
    tee "$SECRETS_DIR/rpc.env" >/dev/null <<RPC
RPC_USER=$RPC_USER
RPC_PASSWORD=$RPC_PASSWORD
RPC

    # Bitcoin config
    tee "$CONFIG_DIR/bitcoin/bitcoin.conf" >/dev/null <<BTCCONF
# Bitcoin Core configuration — Project Truffels
server=1
txindex=1
prune=0
listen=1
maxconnections=128
par=4
dbcache=1024
$RPCAUTH_LINE
rpcbind=0.0.0.0
rpcallowip=172.16.0.0/12
rpcallowip=10.0.0.0/8
zmqpubhashblock=tcp://0.0.0.0:28332
zmqpubrawblock=tcp://0.0.0.0:28334
zmqpubrawtx=tcp://0.0.0.0:28333

# --- Mempool.space Frankfurt peers (better tx relay) ---
addnode=103.99.171.201:8333
addnode=103.99.171.202:8333
addnode=103.99.171.203:8333
addnode=103.99.171.204:8333
addnode=103.99.171.205:8333
addnode=103.99.171.206:8333
BTCCONF

    # Source the credentials we just generated
    source "$SECRETS_DIR/rpc.env"
else
    log "RPC credentials already exist, skipping generation."
    source "$SECRETS_DIR/rpc.env"
fi

# Electrs config
if [[ ! -f "$CONFIG_DIR/electrs/electrs.toml" ]]; then
    tee "$CONFIG_DIR/electrs/electrs.toml" >/dev/null <<ELECTRS
network = "bitcoin"
daemon_rpc_addr = "truffels-bitcoind:8332"
daemon_p2p_addr = "truffels-bitcoind:8333"
auth = "$RPC_USER:$RPC_PASSWORD"
db_dir = "/data/db"
electrum_rpc_addr = "0.0.0.0:50001"
monitoring_addr = "0.0.0.0:4224"
log_filters = "INFO"
ELECTRS
fi

# ckpool config
if [[ ! -f "$CONFIG_DIR/ckpool/ckpool.conf" ]]; then
    # Prompt for mining address if not restoring
    MINING_ADDR="${TRUFFELS_MINING_ADDRESS:-}"
    if [[ -z "$MINING_ADDR" ]]; then
        read -rp "Enter your Bitcoin mining address: " MINING_ADDR
    fi
    MINING_SIG="${TRUFFELS_MINING_SIG:-/truffels/}"

    tee "$CONFIG_DIR/ckpool/ckpool.conf" >/dev/null <<CKPOOL
{
  "btcd": [{"url": "truffels-bitcoind:8332", "auth": "$RPC_USER", "pass": "$RPC_PASSWORD", "notify": true}],
  "zmqblock": "tcp://truffels-bitcoind:28332",
  "blockpoll": 100,
  "logdir": "/data/logs",
  "btcaddress": "$MINING_ADDR",
  "btcsig": "$MINING_SIG",
  "serverurl": ["0.0.0.0:3333"],
  "startdiff": 42,
  "mindiff": 1,
  "maxdiff": 0
}
CKPOOL
fi

# mempool DB credentials
if [[ ! -f "$SECRETS_DIR/mempool-db.env" ]]; then
    MEMPOOL_DB_PASS=$(generate_password)
    tee "$SECRETS_DIR/mempool-db.env" >/dev/null <<MDBENV
MYSQL_ROOT_PASSWORD=$MEMPOOL_DB_PASS
MYSQL_DATABASE=mempool
MYSQL_USER=mempool
MYSQL_PASSWORD=$MEMPOOL_DB_PASS
MDBENV
else
    MEMPOOL_DB_PASS=$(grep MYSQL_PASSWORD "$SECRETS_DIR/mempool-db.env" | cut -d= -f2)
fi

# mempool backend credentials (RPC + DB creds for the backend service)
tee "$SECRETS_DIR/mempool-backend.env" >/dev/null <<MBENV
CORE_RPC_USERNAME=$RPC_USER
CORE_RPC_PASSWORD=$RPC_PASSWORD
DATABASE_USERNAME=mempool
DATABASE_PASSWORD=$MEMPOOL_DB_PASS
MBENV

# ckstats DB credentials
if [[ ! -f "$SECRETS_DIR/ckstats-db.env" ]]; then
    CKSTATS_DB_PASS=$(generate_password)
    tee "$SECRETS_DIR/ckstats-db.env" >/dev/null <<CSDBENV
POSTGRES_USER=ckpool
POSTGRES_PASSWORD=$CKSTATS_DB_PASS
POSTGRES_DB=ckstats
CSDBENV
else
    CKSTATS_DB_PASS=$(grep POSTGRES_PASSWORD "$SECRETS_DIR/ckstats-db.env" | cut -d= -f2)
fi

# ckstats app env
tee "$CONFIG_DIR/ckstats/.env" >/dev/null <<CSENV
API_URL=/ckpool-logs
DB_HOST=truffels-ckstats-db
DB_PORT=5432
DB_USER=ckpool
DB_PASSWORD=$CKSTATS_DB_PASS
DB_NAME=ckstats
DB_SSL=false
DB_SSL_REJECT_UNAUTHORIZED=false
CSENV

# Lock down secrets — group-readable by gid 1000 for API container access
chmod 750 "$SECRETS_DIR"
chown root:1000 "$SECRETS_DIR"
chmod 640 "$SECRETS_DIR"/*.env
chown root:1000 "$SECRETS_DIR"/*.env
chmod 640 "$CONFIG_DIR"/bitcoin/bitcoin.conf "$CONFIG_DIR"/electrs/electrs.toml \
          "$CONFIG_DIR"/ckpool/ckpool.conf "$CONFIG_DIR"/ckstats/.env

# --- Step 6: Compose files ----------------------------------------------------
log "Writing compose files..."

# bitcoind
tee "$COMPOSE_DIR/bitcoin/docker-compose.yml" >/dev/null <<'BITCOINDC'
services:
  bitcoind:
    image: btcpayserver/bitcoin:30.2@sha256:cff45bbc8e166bb3403675baea73cf597c7373f20a87a76101e3d849f766d61e
    container_name: truffels-bitcoind
    restart: unless-stopped
    user: "1000:1000"
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    networks:
      bitcoin-backend:
    ports:
      - "8333:8333"
    volumes:
      - /srv/truffels/data/bitcoin/blockchain:/data
      - /srv/truffels/config/bitcoin/bitcoin.conf:/bitcoin.conf:ro
    entrypoint: ["bitcoind", "-conf=/bitcoin.conf", "-datadir=/data", "-printtoconsole"]
    stop_grace_period: 120s
    deploy:
      resources:
        limits:
          memory: 3500M
    healthcheck:
      test: ["CMD", "bitcoin-cli", "-conf=/bitcoin.conf", "-datadir=/data", "-getinfo"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 60s
networks:
  bitcoin-backend:
    external: true
BITCOINDC

# electrs
tee "$COMPOSE_DIR/electrs/docker-compose.yml" >/dev/null <<'ELECTRSDC'
services:
  electrs:
    image: getumbrel/electrs:v0.11.0@sha256:0a2c6f573abfd8d724651c6ba1c1f3a9c740219c1cf0f4468043c3342170d8a5
    container_name: truffels-electrs
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    networks:
      bitcoin-backend:
    volumes:
      - /srv/truffels/data/electrs:/data
      - /srv/truffels/config/electrs/electrs.toml:/etc/electrs/electrs.toml:ro
    entrypoint: ["electrs", "--conf", "/etc/electrs/electrs.toml"]
    stop_grace_period: 30s
    deploy:
      resources:
        limits:
          memory: 2048M
    healthcheck:
      test: ["CMD-SHELL", "pidof electrs || exit 1"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 120s
networks:
  bitcoin-backend:
    external: true
ELECTRSDC

# ckpool
tee "$COMPOSE_DIR/ckpool/Dockerfile" >/dev/null <<'CKPOOLDKR'
FROM debian:bookworm-slim AS builder
RUN apt-get update && apt-get install -y --no-install-recommends \
    git build-essential autoconf automake libtool pkg-config \
    libczmq-dev ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /build
RUN git clone --depth 1 --branch v1.0.0 https://bitbucket.org/ckolivas/ckpool.git .
RUN ./autogen.sh && ./configure && make -j$(nproc)

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    libczmq4 \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -g 1000 ckpool \
    && useradd -u 1000 -g ckpool -s /usr/sbin/nologin ckpool
COPY --from=builder /build/src/ckpool /usr/local/bin/ckpool
COPY --from=builder /build/src/ckpmsg /usr/local/bin/ckpmsg
COPY --from=builder /build/src/notifier /usr/local/bin/notifier
USER ckpool
ENTRYPOINT ["ckpool"]
CKPOOLDKR

tee "$COMPOSE_DIR/ckpool/docker-compose.yml" >/dev/null <<'CKPOOLDC'
services:
  ckpool:
    build:
      context: .
      dockerfile: Dockerfile
    image: truffels/ckpool:v1.0.0
    container_name: truffels-ckpool
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    networks:
      bitcoin-backend:
    ports:
      - "3333:3333"
    volumes:
      - /srv/truffels/data/ckpool:/data
      - /srv/truffels/config/ckpool/ckpool.conf:/etc/ckpool/ckpool.conf:ro
    entrypoint: ["sh", "-c", "rm -f /tmp/ckpool/main.pid; exec ckpool -B -l 4 -c /etc/ckpool/ckpool.conf"]
    stop_grace_period: 10s
    deploy:
      resources:
        limits:
          memory: 256M
    healthcheck:
      test: ["CMD-SHELL", "pidof ckpool || exit 1"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 30s
networks:
  bitcoin-backend:
    external: true
CKPOOLDC

# mempool
cat > "$COMPOSE_DIR/mempool/docker-compose.yml" <<MEMPOOLDC
services:
  mempool-backend:
    image: $MEMPOOL_BACKEND_IMAGE
    container_name: truffels-mempool-backend
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    networks:
      bitcoin-backend:
    env_file:
      - /srv/truffels/secrets/mempool-backend.env
    environment:
      MEMPOOL_BACKEND: "electrum"
      ELECTRUM_HOST: "truffels-electrs"
      ELECTRUM_PORT: "50001"
      ELECTRUM_TLS_ENABLED: "false"
      CORE_RPC_HOST: "truffels-bitcoind"
      CORE_RPC_PORT: "8332"
      DATABASE_ENABLED: "true"
      DATABASE_HOST: "truffels-mempool-db"
      DATABASE_PORT: "3306"
      DATABASE_DATABASE: "mempool"
      STATISTICS_ENABLED: "true"
    depends_on:
      mempool-db:
        condition: service_healthy
    deploy:
      resources:
        limits:
          memory: 1024M

  mempool-frontend:
    image: $MEMPOOL_FRONTEND_IMAGE
    container_name: truffels-mempool-frontend
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    networks:
      bitcoin-backend:
    environment:
      FRONTEND_HTTP_PORT: "8080"
      BACKEND_MAINNET_HTTP_HOST: "truffels-mempool-backend"
    depends_on:
      - mempool-backend
    deploy:
      resources:
        limits:
          memory: 256M

  mempool-db:
    image: $MARIADB_IMAGE
    container_name: truffels-mempool-db
    restart: unless-stopped
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - DAC_OVERRIDE
      - FOWNER
      - SETGID
      - SETUID
    networks:
      bitcoin-backend:
    env_file:
      - /srv/truffels/secrets/mempool-db.env
    volumes:
      - /srv/truffels/data/mempool/mysql:/var/lib/mysql
    deploy:
      resources:
        limits:
          memory: 512M
    healthcheck:
      test: ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 30s

networks:
  bitcoin-backend:
    external: true
MEMPOOLDC

# ckstats Dockerfile
tee "$COMPOSE_DIR/ckstats/Dockerfile" >/dev/null <<'CKSTATSDKR'
FROM node:20-slim AS builder
RUN corepack enable && corepack prepare pnpm@latest --activate
WORKDIR /app
COPY ckpoolstats/package.json ckpoolstats/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
RUN pnpm add dotenv
COPY ckpoolstats/ ./
RUN sed -i "s|fetch('/api/|fetch('/ckstats/api/|g" components/Header.tsx && \
    sed -i "s|fetch(\`/api/|fetch(\`/ckstats/api/|g" components/UserResetButton.tsx components/PrivacyToggle.tsx && \
    npx prettier --write components/Header.tsx components/UserResetButton.tsx components/PrivacyToggle.tsx
RUN pnpm build

FROM node:20-slim
RUN corepack enable && corepack prepare pnpm@latest --activate
RUN apt-get update && apt-get install -y --no-install-recommends \
    cron postgresql-client \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=builder /app/.next ./.next
COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/package.json ./
COPY --from=builder /app/scripts ./scripts
COPY --from=builder /app/lib ./lib
COPY --from=builder /app/utils ./utils
COPY --from=builder /app/tsconfig.json ./
COPY --from=builder /app/tsconfig.scripts.json ./
COPY --from=builder /app/ormconfig.ts ./
COPY --from=builder /app/next.config.js ./
COPY --from=builder /app/migrations ./migrations
EXPOSE 3000
CMD ["pnpm", "start"]
CKSTATSDKR

tee "$COMPOSE_DIR/ckstats/docker-compose.yml" >/dev/null <<'CKSTATSDC'
services:
  ckstats:
    build:
      context: /srv/truffels/data
      dockerfile: /srv/truffels/compose/ckstats/Dockerfile
    image: truffels/ckstats:latest
    container_name: truffels-ckstats
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    networks:
      bitcoin-backend:
    env_file:
      - /srv/truffels/config/ckstats/.env
    volumes:
      - /srv/truffels/data/ckpool/logs:/ckpool-logs:ro
    depends_on:
      ckstats-db:
        condition: service_healthy
    deploy:
      resources:
        limits:
          memory: 512M
    healthcheck:
      test: ["CMD-SHELL", "node -e 'fetch(\"http://127.0.0.1:3000/ckstats\").then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))'"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 30s

  ckstats-cron:
    image: truffels/ckstats:latest
    container_name: truffels-ckstats-cron
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    networks:
      bitcoin-backend:
    env_file:
      - /srv/truffels/config/ckstats/.env
    volumes:
      - /srv/truffels/data/ckpool/logs:/ckpool-logs:ro
    depends_on:
      ckstats:
        condition: service_healthy
    entrypoint: ["/bin/sh", "-c"]
    command:
      - |
        CLEANUP_COUNTER=0
        while true; do
          echo "[$(date)] Running seed..."
          pnpm seed 2>&1
          echo "[$(date)] Running update-users..."
          pnpm update-users 2>&1
          CLEANUP_COUNTER=$$((CLEANUP_COUNTER + 1))
          if [ "$$CLEANUP_COUNTER" -ge 120 ]; then
            echo "[$(date)] Running cleanup..."
            pnpm cleanup 2>&1
            CLEANUP_COUNTER=0
          fi
          sleep 60
        done
    deploy:
      resources:
        limits:
          memory: 256M

  ckstats-db:
    image: postgres:16.13-alpine@sha256:20edbde7749f822887a1a022ad526fde0a47d6b2be9a8364433605cf65099416
    container_name: truffels-ckstats-db
    restart: unless-stopped
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - DAC_OVERRIDE
      - FOWNER
      - SETGID
      - SETUID
    networks:
      bitcoin-backend:
    env_file:
      - /srv/truffels/secrets/ckstats-db.env
    volumes:
      - /srv/truffels/data/ckstats/postgres:/var/lib/postgresql/data
    deploy:
      resources:
        limits:
          memory: 256M
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ckpool -d ckstats"]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 15s

networks:
  bitcoin-backend:
    external: true
CKSTATSDC

# Caddy proxy
tee "$CONFIG_DIR/proxy/Caddyfile" >/dev/null <<'CADDYFILE'
{
	auto_https off
	admin off
}

:80 {
	# ckstats — basePath handles /ckstats prefix internally
	handle /ckstats* {
		reverse_proxy truffels-ckstats:3000
	}

	# Truffels control plane
	handle /admin* {
		reverse_proxy truffels-web:8080
	}

	handle /api/truffels/* {
		reverse_proxy truffels-api:8080
	}

	# Mempool — all traffic (API + websocket + frontend) goes through
	# the mempool frontend nginx, which handles /api/ → /api/v1/ rewrite.
	# Do NOT route /api/* directly to mempool-backend — Esplora-style paths will 404.
	handle /ws {
		reverse_proxy truffels-mempool-frontend:8080
	}

	handle {
		reverse_proxy truffels-mempool-frontend:8080
	}

	header {
		X-Content-Type-Options nosniff
		X-Frame-Options SAMEORIGIN
		Referrer-Policy no-referrer
		Permissions-Policy "camera=(), microphone=(), geolocation=()"
		-Server
	}
}
CADDYFILE

tee "$COMPOSE_DIR/proxy/docker-compose.yml" >/dev/null <<'PROXYDC'
services:
  proxy:
    image: caddy:2.11.2-alpine@sha256:fce4f15aad23222c0ac78a1220adf63bae7b94355d5ea28eee53910624acedfa
    container_name: truffels-proxy
    restart: unless-stopped
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - DAC_OVERRIDE
      - FOWNER
      - NET_BIND_SERVICE
      - SETGID
      - SETUID
    networks:
      truffels-edge:
      bitcoin-backend:
    ports:
      - "80:80"
    volumes:
      - /srv/truffels/config/proxy/Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config
    deploy:
      resources:
        limits:
          memory: 128M
    healthcheck:
      test: ["CMD", "wget", "--spider", "--quiet", "http://127.0.0.1:80/"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s
volumes:
  caddy_data:
  caddy_config:
networks:
  truffels-edge:
    external: true
  bitcoin-backend:
    external: true
PROXYDC

# --- Step 7: Pull images (unless skipped) -------------------------------------
if [[ "$SKIP_PULL" == false ]]; then
    log "Pulling Docker images..."
    docker pull "$BITCOIN_IMAGE"
    docker pull "$ELECTRS_IMAGE"
    docker pull "$MEMPOOL_BACKEND_IMAGE"
    docker pull "$MEMPOOL_FRONTEND_IMAGE"
    docker pull "$MARIADB_IMAGE"
    docker pull "$POSTGRES_IMAGE"
    docker pull "$CADDY_IMAGE"
fi

# --- Step 8: Build custom images ---------------------------------------------
log "Building ckpool image..."
cd "$COMPOSE_DIR/ckpool" && docker compose build --quiet

log "Building ckstats image..."
cd "$COMPOSE_DIR/ckstats" && docker compose build --quiet

# --- Step 9: Start services in order ------------------------------------------
log "Starting bitcoind..."
cd "$COMPOSE_DIR/bitcoin" && docker compose up -d

log "Waiting for bitcoind to become healthy..."
timeout 300 bash -c 'until docker inspect truffels-bitcoind --format="{{.State.Health.Status}}" 2>/dev/null | grep -q healthy; do sleep 5; done' \
    || warn "bitcoind not healthy yet — it may still be syncing. Continuing anyway."

log "Starting electrs..."
cd "$COMPOSE_DIR/electrs" && docker compose up -d

log "Starting ckpool..."
cd "$COMPOSE_DIR/ckpool" && docker compose up -d

log "Starting mempool..."
cd "$COMPOSE_DIR/mempool" && docker compose up -d

log "Starting ckstats..."
cd "$COMPOSE_DIR/ckstats" && docker compose up -d ckstats-db
sleep 5
# Run migrations
docker compose -f "$COMPOSE_DIR/ckstats/docker-compose.yml" run --rm ckstats pnpm migration:run
docker compose -f "$COMPOSE_DIR/ckstats/docker-compose.yml" up -d

log "Starting reverse proxy..."
cd "$COMPOSE_DIR/proxy" && docker compose up -d

# --- Step 9b: Truffels control plane ------------------------------------------
log "Writing truffels control plane compose..."

TRUFFELS_API_SRC="${TRUFFELS_API_SRC:-/home/truffel/Project-Truffels/truffels-api}"
TRUFFELS_WEB_SRC="${TRUFFELS_WEB_SRC:-/home/truffel/Project-Truffels/truffels-web}"
TRUFFELS_AGENT_SRC="${TRUFFELS_AGENT_SRC:-/home/truffel/Project-Truffels/truffels-agent}"

tee "$COMPOSE_DIR/truffels/docker-compose.yml" >/dev/null <<TRUFFELSDC
services:
  agent:
    build:
      context: $TRUFFELS_AGENT_SRC
      dockerfile: $TRUFFELS_AGENT_SRC/Dockerfile
    image: truffels/agent:v0.1.0
    container_name: truffels-agent
    pid: "host"
    cap_add:
      - SYS_ADMIN
      - SYS_PTRACE
    restart: unless-stopped
    networks:
      truffels-core:
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /srv/truffels/compose:/srv/truffels/compose:ro
      - /srv/truffels/config:/srv/truffels/config:ro
      - /srv/truffels/secrets:/srv/truffels/secrets:ro
    environment:
      TRUFFELS_COMPOSE_ROOT: "/srv/truffels/compose"
      TRUFFELS_AGENT_LISTEN: ":9090"
    deploy:
      resources:
        limits:
          memory: 128M
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:9090/v1/health"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s

  api:
    build:
      context: $TRUFFELS_API_SRC
      dockerfile: $TRUFFELS_API_SRC/Dockerfile
    image: truffels/api:v0.1.0
    container_name: truffels-api
    user: "1000:1000"
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    networks:
      bitcoin-backend:
      truffels-edge:
      truffels-core:
    depends_on:
      agent:
        condition: service_healthy
    volumes:
      - /srv/truffels/config:/srv/truffels/config:ro
      - /srv/truffels/compose:/srv/truffels/compose
      - /srv/truffels/secrets:/srv/truffels/secrets:ro
      - /srv/truffels/data/truffels:/data
      - /srv/truffels/data:/srv/truffels/data:ro
      - /srv/truffels/backups:/srv/truffels/backups
      - /proc:/host/proc:ro
      - /sys:/host/sys:ro
    environment:
      TRUFFELS_LISTEN: ":8080"
      TRUFFELS_DB_PATH: "/data/truffels.db"
      TRUFFELS_COMPOSE_ROOT: "/srv/truffels/compose"
      TRUFFELS_CONFIG_ROOT: "/srv/truffels/config"
      TRUFFELS_SECRETS_ROOT: "/srv/truffels/secrets"
      TRUFFELS_DATA_ROOT: "/srv/truffels/data"
      TRUFFELS_HOST_PROC: "/host/proc"
      TRUFFELS_HOST_SYS: "/host/sys"
      TRUFFELS_AGENT_URL: "http://truffels-agent:9090"
    deploy:
      resources:
        limits:
          memory: 256M
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/api/truffels/health"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 15s

  web:
    build:
      context: $TRUFFELS_WEB_SRC
      dockerfile: $TRUFFELS_WEB_SRC/Dockerfile
    image: truffels/web:v0.1.0
    container_name: truffels-web
    restart: unless-stopped
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - DAC_OVERRIDE
      - FOWNER
      - NET_BIND_SERVICE
      - SETGID
      - SETUID
    networks:
      truffels-edge:
    deploy:
      resources:
        limits:
          memory: 64M
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/admin/"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s

networks:
  bitcoin-backend:
    external: true
  truffels-edge:
    external: true
  truffels-core:
    external: true
TRUFFELSDC

log "Building truffels-agent image..."
cd "$COMPOSE_DIR/truffels" && docker compose build agent --quiet

log "Building truffels-api image..."
cd "$COMPOSE_DIR/truffels" && docker compose build api --quiet

log "Building truffels-web image..."
cd "$COMPOSE_DIR/truffels" && docker compose build web --quiet

log "Starting truffels control plane..."
cd "$COMPOSE_DIR/truffels" && docker compose up -d

# --- Step 9c: NVMe swap ------------------------------------------------------
if [[ ! -f "$TRUFFELS_BASE/swapfile" ]]; then
    log "Creating 4GB NVMe swap file..."
    fallocate -l 4G "$TRUFFELS_BASE/swapfile"
    chmod 600 "$TRUFFELS_BASE/swapfile"
    mkswap "$TRUFFELS_BASE/swapfile"
    swapon "$TRUFFELS_BASE/swapfile"
    if ! grep -q "$TRUFFELS_BASE/swapfile" /etc/fstab; then
        echo "$TRUFFELS_BASE/swapfile none swap sw,pri=10 0 0" >> /etc/fstab
    fi
    log "NVMe swap enabled (4GB, priority 10 — overflow after zram)"
else
    log "Swap file already exists, skipping."
    swapon "$TRUFFELS_BASE/swapfile" 2>/dev/null || true
fi

# --- Step 10: Host firewall (nftables) ----------------------------------------
log "Configuring host firewall..."
tee "$CONFIG_DIR/nftables/truffels.conf" >/dev/null <<'NFTCONF'
#!/usr/sbin/nft -f
# Project Truffels — Host firewall rules
# Only filters INPUT to host services. Docker manages its own FORWARD chains.

table inet truffels_firewall {
  chain input {
    type filter hook input priority 0; policy drop;

    # Loopback — always allow
    iif lo accept

    # Established/related — allow return traffic
    ct state established,related accept

    # SSH
    tcp dport 22 accept

    # Caddy reverse proxy (HTTP)
    tcp dport 80 accept

    # Bitcoin P2P
    tcp dport 8333 accept

    # ckpool stratum (LAN miners)
    tcp dport 3333 accept

    # ICMP ping
    ip protocol icmp accept
    ip6 nexthdr icmpv6 accept

    # Docker bridge traffic (container → host)
    iifname "br-*" accept
    iifname "docker0" accept
  }
}
NFTCONF

nft -f "$CONFIG_DIR/nftables/truffels.conf"

tee /etc/systemd/system/truffels-firewall.service >/dev/null <<'FWSVC'
[Unit]
Description=Truffels host firewall (nftables)
After=docker.service
Wants=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft -f /srv/truffels/config/nftables/truffels.conf
ExecStop=/usr/sbin/nft delete table inet truffels_firewall

[Install]
WantedBy=multi-user.target
FWSVC

systemctl daemon-reload
systemctl enable truffels-firewall.service
log "Host firewall active — INPUT drop policy, allow SSH/80/3333/8333"

# --- Step 11: Verify ----------------------------------------------------------
log "Waiting for services to stabilize..."
sleep 15

log "Container status:"
docker ps --format "table {{.Names}}\t{{.Status}}"

log ""
log "Ports exposed to LAN:"
ss -tlnp | grep -E ':(22|80|3333|8333) ' | awk '{print $4}' | sort -u

LAN_IP=$(ip -4 addr show scope global | grep -oP '(?<=inet )\d+\.\d+\.\d+\.\d+' | head -1)
log ""
log "============================================"
log " Project Truffels installation complete!"
log "============================================"
log ""
log " Admin UI: http://$LAN_IP/admin/"
log " API:      http://$LAN_IP/api/truffels/health"
log " Mempool:  http://$LAN_IP/"
log " ckstats:  http://$LAN_IP/ckstats/"
log " Stratum:  $LAN_IP:3333"
log ""
log " Secrets:  $SECRETS_DIR/"
log " Configs:  $CONFIG_DIR/"
log " Data:     $DATA_DIR/"
log ""
log " Note: electrs reindexing may take 8-12 hours."
log " Note: ckpool requires bitcoind fully synced."
log ""

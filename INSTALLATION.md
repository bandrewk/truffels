# Project Truffels Installation Runbook

This file is the **operational install log and command reference** for Project Truffels.

It is intentionally practical and incremental.
It records:
- the agreed installation order
- the exact commands used
- required verification steps
- known platform quirks on Raspberry Pi 5
- what is already done and what comes next

This is **not** the high-level architecture spec.
For product scope and design decisions, see `Project_Truffels_Spec.md`.
For migration from the proof of concept node, see `MIGRATION.md`.

---

## 0. Current baseline status

Current host target:
- hardware: Raspberry Pi 5 8 GB
- storage: Samsung 990 PRO 2 TB NVMe on Geekworm X1001
- OS target: Raspberry Pi OS Lite 64-bit on NVMe
- hostname: `truffels`
- timezone: `Europe/Berlin`
- boot source: NVMe
- stability baseline: PCIe Gen 2 x1, no forced Gen 3

Current verified state:
- root filesystem is on `/dev/nvme0n1p2`
- `/boot/firmware` is on `/dev/nvme0n1p1`
- no visible throttling (`throttled=0x0` during baseline checks)
- memory cgroups are active at runtime

Runtime cgroup acceptance check:

```bash
cat /sys/fs/cgroup/cgroup.controllers
```

Expected minimum acceptable output:

```text
cpuset cpu io memory pids
```

---

## 1. Known Raspberry Pi 5 platform quirk: memory cgroups

Observed state during bring-up:
- `/proc/cmdline` included `cgroup_disable=memory`
- adding `cgroup_enable=memory` restored active memory cgroups
- runtime result mattered more than a pretty command line

Verification commands:

```bash
echo '### EFFECTIVE CMDLINE'; cat /proc/cmdline
echo
echo '### CGROUP CONTROLLERS'; cat /sys/fs/cgroup/cgroup.controllers
echo
echo '### PROC CGROUPS'; cat /proc/cgroups
```

Rule:
- if `memory` is **missing** from `cgroup.controllers`, stop and fix boot configuration before Docker installation
- if `memory` is **present**, proceed

---

## 2. Host baseline verification

Run before installing Docker or restoring services:

```bash
echo '### HOSTNAME'; hostname; hostnamectl --static
echo

echo '### OS'; cat /etc/os-release | sed -n '1,8p'
echo

echo '### KERNEL'; uname -a
echo

echo '### ROOT AND BOOT MOUNTS'; findmnt /; findmnt /boot/firmware
echo

echo '### BLOCK DEVICES'; lsblk -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS,MODEL
echo

echo '### FSTAB'; sudo sh -c "grep -v '^\s*#' /etc/fstab | sed '/^\s*$/d'"
echo

echo '### CONFIG.TXT PCIE/NVME'; sudo grep -niE 'pcie|nvme|gen' /boot/firmware/config.txt || true
echo

echo '### CMDLINE'; cat /boot/firmware/cmdline.txt
echo

echo '### EEPROM BOOT ORDER'; sudo rpi-eeprom-config | grep -i BOOT_ORDER || true
echo

echo '### THROTTLE'; vcgencmd get_throttled || true
echo

echo '### RECENT NVME/PCIE ERRORS'; sudo dmesg -T | grep -iE 'nvme|pcie|aer|timeout|reset|voltage|thrott' | tail -n 80
```

Acceptance:
- `/` must be on NVMe
- `/boot/firmware` must be on NVMe
- no forced PCIe Gen 3 setting for V1
- no active undervoltage or throttling
- no repeated NVMe reset or PCIe timeout errors

---

## 3. Host maintenance baseline

```bash
sudo apt update
sudo apt full-upgrade -y
sudo apt autoremove -y
sudo timedatectl set-timezone Europe/Berlin
timedatectl
df -h
```

Optional extra checks:

```bash
free -h
swapon --show
journalctl --disk-usage
systemctl is-enabled ssh
systemctl is-active ssh
```

---

## 4. Docker installation

> Use the official Docker APT repository.
> Do **not** use `docker.io` for the Truffels V1 baseline.

### 4.1 Remove conflicting packages if present

```bash
sudo apt remove -y docker.io docker-compose docker-doc podman-docker containerd runc || true
```

### 4.2 Install prerequisites

```bash
sudo apt update
sudo apt install -y ca-certificates curl
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
```

### 4.3 Add the official Docker repository

```bash
sudo tee /etc/apt/sources.list.d/docker.sources >/dev/null <<EOF2
Types: deb
URIs: https://download.docker.com/linux/debian
Suites: $(. /etc/os-release && echo "$VERSION_CODENAME")
Components: stable
Signed-By: /etc/apt/keyrings/docker.asc
EOF2
```

### 4.4 Install Docker Engine + Compose plugin

```bash
sudo apt update
sudo apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
```

### 4.5 Verify Docker installation

```bash
sudo systemctl status docker --no-pager
sudo docker run hello-world
sudo docker version
docker compose version
```

Rule for V1:
- use `sudo docker` initially
- do **not** add the main user to the `docker` group until the hardening baseline is decided

---

## 5. Docker daemon baseline

Create daemon config:

```bash
sudo install -d -m 0755 /etc/docker
sudo tee /etc/docker/daemon.json >/dev/null <<'EOF2'
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
EOF2
```

Restart and verify:

```bash
sudo systemctl restart docker
sudo systemctl status docker --no-pager
```

Notes:
- `live-restore` reduces disruption when the daemon restarts
- `local` log driver is preferred over unbounded json logs for this appliance use case

---

## 6. Truffels host directory layout

Create the baseline layout on the NVMe root filesystem:

```bash
sudo mkdir -p /srv/truffels/{compose,config,data,logs,backups,secrets,tmp}
sudo chmod 0755 /srv/truffels
sudo chmod 0750 /srv/truffels/secrets
```

Recommended subpaths for V1:

```text
/srv/truffels/compose
/srv/truffels/config
/srv/truffels/data/bitcoin
/srv/truffels/data/ckpool
/srv/truffels/data/electrs
/srv/truffels/data/mempool
/srv/truffels/logs
/srv/truffels/backups
/srv/truffels/secrets
/srv/truffels/tmp
```

---

## 7. Restore of migrated data

> Only restore data after Docker and the directory layout are in place.
> The existing fully synced blockchain must be reused.

### 7.1 Create target directories

```bash
sudo mkdir -p /srv/truffels/data/bitcoin
sudo mkdir -p /srv/truffels/data/ckpool
sudo mkdir -p /srv/truffels/data/electrs
sudo mkdir -p /srv/truffels/data/mempool
```

### 7.2 Restore from the temporary migration backup disk

If the temporary backup disk was exFAT or NTFS, restore with a normal rsync copy first:

```bash
sudo rsync -rltDvh --info=progress2 /mnt/external/truffels-prewipe/backups/nvme/bitcoin/ /srv/truffels/data/bitcoin/
sudo rsync -rltDvh --info=progress2 /mnt/external/truffels-prewipe/backups/nvme/ckpool/ /srv/truffels/data/ckpool/
sudo rsync -rltDvh --info=progress2 /mnt/external/truffels-prewipe/backups/nvme/electrs/ /srv/truffels/data/electrs/ || true
sudo rsync -rltDvh --info=progress2 /mnt/external/truffels-prewipe/backups/nvme/ckpoolstats/ /srv/truffels/data/ckpoolstats/ || true
```

### 7.3 Reapply ownership on the final ext4 target

These ownerships are examples based on the legacy PoC users and may later be replaced by container user mappings:

```bash
sudo chown -R 1000:1000 /srv/truffels/data || true
```

For manual service inspection before containerization, ownership can be adjusted later per service.
Do **not** overfit permanent ownership rules before the container design is finalized.

---

## 8. Current installation order for Truffels V1

Strict order:

1. ~~Host baseline verification~~ ✓
2. ~~Confirm memory cgroups active~~ ✓
3. ~~Install Docker from official Docker repository~~ ✓
4. ~~Apply Docker daemon baseline~~ ✓
5. ~~Create `/srv/truffels` layout~~ ✓
6. ~~Restore blockchain and migrated service data~~ ✓
7. ~~Create `bitcoind` container stack~~ ✓ (step 9)
8. ~~Verify bitcoind uses restored datadir and does not resync from zero~~ ✓
9. ~~Add `electrs`~~ ✓ (step 10, reindex required — old index incompatible)
10. ~~Add `mempool`~~ ✓ (step 12)
11. ~~Add `ckpool`~~ ✓ (step 11, custom Docker image built)
12. ~~Add reverse proxy + network segmentation~~ ✓ (step 13)
13. ~~Add truffels-api (Go control plane backend)~~ ✓ (step 14)
14. ~~Add truffels-web (React control plane UI)~~ ✓ (step 15)
15. ~~Add NVMe swap~~ ✓ (step 16)
16. ~~Hardening (auth, secrets, firewall, Docker hardening, backups)~~ ✓ (step 17)
17. ~~Add truffels-agent (privileged Docker mediator)~~ ✓ (step 18)
18. ~~CI pipeline (GitHub Actions, 310+ tests)~~ ✓ (step 19)
19. Add ePaper renderer last (ping user first)

---

## 9. Bitcoind container stack

Completed: 2026-03-09

### 9.1 Docker network

```bash
sudo docker network create --driver bridge --subnet 172.20.0.0/24 bitcoin-backend
```

### 9.2 RPC credentials

Generated with `rpcauth` (HMAC-SHA256). Stored in:
- `/srv/truffels/config/bitcoin/bitcoin.conf` — rpcauth line
- `/srv/truffels/secrets/rpc.env` — username + password for dependent services

Format reminder: `rpcauth=username:salt$hash` (colon between username and salt).

### 9.3 Bitcoin configuration

The installer prompts for pruning preference before generating `bitcoin.conf`:

- **Full node (default, enter `0`):** `prune=0`, `txindex=1` — all services available (electrs, mempool, ckpool). Requires ~650GB disk.
- **Pruned node (enter `550` or higher):** `prune=N` (N = MiB of blocks to keep) — electrs and mempool are unavailable (blocked by API-level `RequiresUnpruned` check), ckpool works fine. Saves significant disk space.

To skip the prompt in non-interactive installs, set `TRUFFELS_PRUNE_SIZE` env var (e.g., `TRUFFELS_PRUNE_SIZE=0 sudo ./install.sh`).

File: `/srv/truffels/config/bitcoin/bitcoin.conf`

Key settings:
- `server=1` (explicit RPC server enable — implied by `rpcauth` but clearer stated)
- `txindex=1`, `prune=0` (full node, required by electrs) — or `prune=N` with txindex disabled if pruned mode selected
- `dbcache=1024` (UTXO cache — sufficient post-sync, saves ~1GB RAM vs 2048)
- `maxconnections=128`
- RPC bound to `0.0.0.0` with `rpcallowip` restricted to Docker network ranges
- ZMQ on `0.0.0.0:{28332,28333,28334}` (internal Docker network only)
- `addnode` entries for 6 mempool.space Frankfurt nodes (103.99.171.201–206) — improves transaction relay and mempool fullness for more accurate fee/block projections
- No `daemon=1` (foreground mode for Docker)

### 9.4 Compose file

File: `/srv/truffels/compose/bitcoin/docker-compose.yml`

- Image: `btcpayserver/bitcoin:30.2` (arm64, digest pinned)
- Container: `truffels-bitcoind`
- User: `1000:1000` (matches data ownership)
- Network: `bitcoin-backend`
- Port exposed: `8333` (P2P)
- Volumes: data at `/srv/truffels/data/bitcoin/blockchain`, config read-only bind
- Healthcheck: `bitcoin-cli -getinfo` every 30s
- Memory limit: 3500M
- Restart: `unless-stopped`

### 9.5 Start and verify

```bash
cd /srv/truffels/compose/bitcoin && sudo docker compose up -d
sudo docker logs truffels-bitcoind --tail 20
sudo docker exec truffels-bitcoind bitcoin-cli -conf=/bitcoin.conf -datadir=/data -getinfo
```

Verification:
- Loaded existing chain at block 939653 (no resync from zero)
- RPC authentication working via `rpcauth`
- Caught up to tip within ~30 minutes
- Healthcheck: `healthy`

### 9.6 Known issues during setup

- Bitcoin Core reads `<datadir>/bitcoin.conf` by default — always use explicit `-conf=` flag
- Test runs may create files owned by root (uid 999) in the datadir — chown after any test
- `rpcauth` format uses colon separator (`user:salt$hash`), not dollar sign between user and salt

---

## 10. electrs container stack

Completed: 2026-03-09

### 10.1 Image

`getumbrel/electrs:v0.11.0` (arm64, digest pinned)

### 10.2 Index compatibility

The 57GB index from the PoC was **incompatible** (RocksDB `format_version: 6` from a source-compiled build vs the packaged image). Index was deleted and rebuilt from scratch.

### 10.3 Configuration

File: `/srv/truffels/config/electrs/electrs.toml`

- `daemon_rpc_addr = "truffels-bitcoind:8332"` (Docker DNS)
- auth using rpcauth credentials from `/srv/truffels/secrets/rpc.env`
- `db_dir = "/data/db"`
- `electrum_rpc_addr = "0.0.0.0:50001"` (internal network only)
- `monitoring_addr = "0.0.0.0:4224"` (for healthcheck)

### 10.4 Compose file

File: `/srv/truffels/compose/electrs/docker-compose.yml`

- Container: `truffels-electrs`
- Network: `bitcoin-backend`
- Volume: `/srv/truffels/data/electrs:/data`
- Healthcheck: `pidof electrs` (`nc` not available in minimal image)
- Memory limit: 2048M
- No ports exposed to host

### 10.5 Start and verify

```bash
cd /srv/truffels/compose/electrs && sudo docker compose up -d
sudo docker logs truffels-electrs --tail 20
```

Note: electrs waits for bitcoind to finish IBD before indexing starts. Full index build takes ~8-12 hours on Pi 5.

---

## 11. ckpool container stack

Completed: 2026-03-09

### 11.1 Image

Custom build — no official Docker image exists for ckpool.

Dockerfile: `/srv/truffels/compose/ckpool/Dockerfile`
- Multi-stage build from `debian:bookworm-slim`
- Source: `bitbucket.org/ckolivas/ckpool` tag `v1.0.0`
- Runtime: minimal Debian with `libczmq4`
- Runs as uid 1000

```bash
cd /srv/truffels/compose/ckpool && sudo docker build -t truffels/ckpool:v1.0.0 .
```

### 11.2 Configuration

File: `/srv/truffels/config/ckpool/ckpool.conf`

- RPC target: `truffels-bitcoind:8332` (Docker DNS)
- ZMQ: `tcp://truffels-bitcoind:28332`
- Mining address: `<your-btc-address>`
- Signature: `/mined by the king fam/`
- Stratum bind: `0.0.0.0:3333`

### 11.3 Compose file

File: `/srv/truffels/compose/ckpool/docker-compose.yml`

- Container: `truffels-ckpool`
- Network: `bitcoin-backend`
- Port exposed: `3333` (stratum — LAN access for miners)
- Volume: `/srv/truffels/data/ckpool:/data`
- Healthcheck: `pidof ckpool` (`nc` not available in minimal image)
- Memory limit: 256M

### 11.4 Start and verify

```bash
cd /srv/truffels/compose/ckpool && sudo docker compose up -d
sudo docker logs truffels-ckpool --tail 20
sudo ss -tlnp | grep 3333
```

Note: ckpool requires bitcoind to be fully synced (`getblocktemplate` fails during IBD).

---

## 12. mempool container stack

Completed: 2026-03-09

### 12.1 Images

- `mempool/backend:v3.2.1` (arm64, digest pinned)
- `mempool/frontend:v3.2.1` (arm64, digest pinned)
- `mariadb:lts` (arm64, digest pinned)

### 12.2 Database

MariaDB sidecar with credentials in `/srv/truffels/secrets/mempool-db.env`.
Data persisted to `/srv/truffels/data/mempool/mysql`.

### 12.3 Compose file

File: `/srv/truffels/compose/mempool/docker-compose.yml`

Three services:
- `truffels-mempool-backend` — connects to bitcoind (RPC) and electrs (electrum RPC)
- `truffels-mempool-frontend` — nginx serving the web UI, also proxies `/api/` → `/api/v1/` to backend
- `truffels-mempool-db` — MariaDB with healthcheck

All on `bitcoin-backend` network. No ports exposed to host (exposed via reverse proxy).

### 12.3a Secrets

Credentials split across two env files:
- `/srv/truffels/secrets/mempool-db.env` — MariaDB root/user passwords (used by `mempool-db` service)
- `/srv/truffels/secrets/mempool-backend.env` — RPC + DB credentials (used by `mempool-backend` service)

No credentials should appear in `docker-compose.yml` — only `env_file:` references.

### 12.3b Critical routing note

The mempool frontend nginx rewrites `/api/block/...` → `/api/v1/block/...` before proxying to the backend. The mempool backend only serves under `/api/v1/` — it returns 404 for bare `/api/block/...` requests. Therefore **all mempool traffic (API, websocket, frontend) must route through the mempool frontend container**, not directly to the backend. If the reverse proxy sends `/api/*` directly to the backend, block/tx detail pages will show "Error loading data".

### 12.4 Start and verify

```bash
cd /srv/truffels/compose/mempool && sudo docker compose up -d
sudo docker logs truffels-mempool-backend --tail 20
```

Note: mempool backend requires both bitcoind and electrs to be fully operational for full functionality.

---

## 12b. ckstats container stack (mining stats dashboard)

Completed: 2026-03-10

### 12b.1 Image

Custom build from the restored `ckpoolstats` source (Next.js 14, pnpm, TypeORM).

Dockerfile: `/srv/truffels/compose/ckstats/Dockerfile`
- Multi-stage build: Node 20 builder + slim runtime
- `dotenv` added as missing dependency
- Includes cron/postgresql-client for scheduled tasks

### 12b.2 Stack

Three containers:
- `truffels-ckstats` — Next.js web dashboard (port 3000 internal)
- `truffels-ckstats-cron` — runs `seed` and `update-users` every 60s, `cleanup` every 2 hours
- `truffels-ckstats-db` — PostgreSQL 16.13 Alpine

### 12b.3a basePath configuration

The Next.js app requires `basePath: '/ckstats'` in `next.config.js` so that all asset URLs (CSS, JS under `/_next/static/`) are prefixed with `/ckstats`. Without this, the reverse proxy routes asset requests to the wrong backend (mempool instead of ckstats), causing white-page / MIME type errors.

**Important:** With `basePath` set, the Caddy reverse proxy must NOT use `uri strip_prefix /ckstats` — Next.js handles the prefix internally. The healthcheck URL must also include the basePath: `http://127.0.0.1:3000/ckstats`.

**Important:** Next.js `basePath` does NOT apply to raw `fetch()` calls in client components. The Dockerfile patches 3 component files at build time (sed + prettier) to prefix `/api/` with `/ckstats/api/`, preventing requests from being routed to mempool via Caddy's catch-all. This patch is applied automatically on every build, keeping the source untouched.

### 12b.3b Cleanup cron

The cron container runs a cleanup job every ~2 hours (120 iterations × 60s sleep). In `docker-compose.yml`, shell variables must be escaped with `$$` to prevent Compose from interpreting them as environment variable references.

### 12b.3 Configuration

- App env: `/srv/truffels/config/ckstats/.env`
- DB credentials: `/srv/truffels/secrets/ckstats-db.env`
- Reads ckpool logs from `/srv/truffels/data/ckpool/logs` (read-only bind)

### 12b.4 Start and verify

```bash
cd /srv/truffels/compose/ckstats
sudo docker compose up -d ckstats-db
sleep 5
sudo docker compose run --rm ckstats pnpm migration:run
sudo docker compose up -d
```

Accessible via proxy at `http://192.168.0.196/ckstats/`

---

## 13. Reverse proxy (Caddy)

Completed: 2026-03-10

### 13.1 Network

```bash
sudo docker network create --driver bridge --subnet 172.21.0.0/24 truffels-edge
```

### 13.2 Image

`caddy:2.11.2-alpine` (arm64, digest pinned)

### 13.3 Caddyfile

File: `/srv/truffels/config/proxy/Caddyfile`

- `auto_https off` — LAN-only, no Let's Encrypt
- `admin off` — no Caddy admin API
- ckstats proxied at `/ckstats*` (no `strip_prefix` — Next.js `basePath` handles it)
- Admin UI at `/admin*` → `truffels-web:8080`
- Truffels API at `/api/truffels/*` → `truffels-api:8080`
- **All other traffic (including `/api/*`, `/ws`, and `/`) → `truffels-mempool-frontend:8080`**
  - The mempool frontend nginx handles `/api/` → `/api/v1/` rewrite internally
  - Do NOT route `/api/*` directly to the mempool backend — it will 404 on Esplora-style paths
- Security headers: `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, `Permissions-Policy`, `Server` stripped

### 13.4 Compose file

File: `/srv/truffels/compose/proxy/docker-compose.yml`

- Container: `truffels-proxy`
- Networks: `truffels-edge` + `bitcoin-backend` (bridges user-facing traffic to backend services)
- Port exposed: `80`
- Healthcheck: `wget --spider http://127.0.0.1:80/`
- Memory limit: 128M

### 13.5 LAN access

- Mempool UI: `http://192.168.0.196/`
- ckstats: `http://192.168.0.196/ckstats/`
- Admin UI: `http://192.168.0.196/admin/`
- API health: `http://192.168.0.196/api/truffels/health`

### 13.6 Ports exposed to LAN after Phase 5

| Port | Service | Purpose |
|------|---------|---------|
| 22 | SSH | Administration |
| 80 | Caddy | Web UI (mempool) |
| 3333 | ckpool | Stratum (miners) |
| 8333 | bitcoind | P2P (peers) |

No RPC, ZMQ, electrum, or database ports are exposed.

---

## 14. truffels-api (Go control plane backend)

Completed: 2026-03-10

### 14.1 Image

Custom build — Go 1.24 multi-stage, runtime on `debian:bookworm-slim` with Docker CLI + Compose plugin.

Source: `/home/truffel/Project-Truffels/truffels-api/`

### 14.2 What it does

- REST API at `/api/truffels/*` (behind Caddy proxy)
- Host metrics: CPU, RAM, temperature, disk (from bind-mounted `/proc` and `/sys`)
- Service management: list/start/stop/restart via `docker compose` CLI
- Service dependency graph enforcement
- Config file viewing and editing with revision history (SQLite)
- Alert engine (disk full, high temp, unhealthy containers, restart loops) — runs every 30s
- Update engine (Docker Hub / GitHub / Bitbucket version checks, apply with rollback) — runs every 24h

### 14.3 Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/truffels/health` | Health check |
| GET | `/api/truffels/dashboard` | System overview |
| GET | `/api/truffels/services` | All services with container states |
| GET | `/api/truffels/services/:id` | Single service detail |
| POST | `/api/truffels/services/:id/action` | Start/stop/restart |
| GET | `/api/truffels/services/:id/logs` | Container logs |
| GET | `/api/truffels/services/:id/config` | Config + revisions |
| POST | `/api/truffels/services/:id/config` | Update config |
| GET | `/api/truffels/host` | Host metrics |
| GET | `/api/truffels/alerts` | Active alerts |
| GET | `/api/truffels/updates` | All update checks |
| GET | `/api/truffels/updates/status` | Update status with sources |
| POST | `/api/truffels/updates/check` | Trigger manual update check |
| GET | `/api/truffels/updates/preflight/:id` | Run preflight checks before update |
| POST | `/api/truffels/updates/apply/:id` | Apply update for a service |
| POST | `/api/truffels/updates/apply-all` | Apply all pending updates |
| GET | `/api/truffels/updates/logs` | Update history log |

### 14.4 Compose file

File: `/srv/truffels/compose/truffels/docker-compose.yml`

- Container: `truffels-api`
- Networks: `bitcoin-backend` + `truffels-edge`
- Volumes: Docker socket, `/srv/truffels`, `/proc`, `/sys` (read-only), SQLite at `/data`
- Healthcheck: `wget -qO-` (chi router only handles GET, not HEAD from `--spider`)
- Memory limit: 256M

### 14.5 Known issues

- `wget --spider` sends HEAD — chi router returns 405, use `wget -qO-` for healthchecks
- Compose directory for bitcoind is `bitcoin/`, not `bitcoind/` — template uses explicit `ComposeDir` field
- Go not installed on host — all builds happen inside Docker multi-stage

---

## 15. truffels-web (React control plane UI)

Completed: 2026-03-10

### 15.1 Image

Custom build — Node 20 builder + `nginx:alpine` runtime.

Source: `/home/truffel/Project-Truffels/truffels-web/`

### 15.2 Stack

React 18, TypeScript, Vite 6, Tailwind CSS 3. Dark-mode-first.

### 15.3 Pages

- **Dashboard:** CPU/RAM/temp/disk bars, uptime, service status cards, active alerts
- **Services:** Card grid with dependency tags, container-level health
- **Service Detail:** Overview (containers, info with version + update status), Logs (scrollable, per-container filtering for shared stacks with smart default), Config (viewer + revision history), Start/Stop/Restart actions
- **Alerts:** Active and resolved alert history
- **Updates:** Service update cards with version info, Check Now / Update All actions, source links (Docker Hub / GitHub / Bitbucket), update history log

Responsive header with hamburger menu on mobile. Auto-refreshes every 10 seconds. All data from truffels-api.

### 15.4 Deployment

- Static files built into `/usr/share/nginx/html/admin/` in container
- Served at `/admin/` via nginx on port 8080 (internal)
- Caddy proxies `/admin*` to `truffels-web:8080`
- Container: `truffels-web`, 64M memory limit
- Network: `truffels-edge`

### 15.5 Known issues

- Vite `base: '/admin/'` means built assets go to `dist/assets/` — Dockerfile copies to `nginx/html/admin/` so nginx path matches
- Without this, assets return `text/html` (same pattern as ckstats basePath fix)
- nginx `absolute_redirect` must be `off` — otherwise the `301 /admin → /admin/` redirect includes nginx's internal port (8080) instead of the external port (80), breaking the redirect through Caddy

### 15.6 Start and verify

```bash
cd /srv/truffels/compose/truffels
sudo docker compose build
sudo docker compose up -d
```

Accessible via proxy at `http://192.168.0.196/admin/`

---

## 16. NVMe swap

Completed: 2026-03-10

8GB RAM is tight with 13 containers plus development tools. Added NVMe-backed swap as overflow after zram.

### 16.1 Setup

```bash
sudo fallocate -l 4G /srv/truffels/swapfile
sudo chmod 600 /srv/truffels/swapfile
sudo mkswap /srv/truffels/swapfile
sudo swapon /srv/truffels/swapfile
echo '/srv/truffels/swapfile none swap sw,pri=10 0 0' | sudo tee -a /etc/fstab
```

### 16.2 Swap priority

| Swap | Size | Priority | Role |
|------|------|----------|------|
| zram0 | 2 GB | 100 (highest) | Primary — compressed RAM |
| NVMe swapfile | 4 GB | 10 | Overflow — Samsung 990 PRO |

Total: 6 GB swap + 8 GB RAM = ~14 GB usable.

### 16.3 Verify

```bash
swapon --show
free -h
```

---

## 17. Hardening (Phase 10)

Completed: 2026-03-10

### 17.1 Authentication

Admin password + session cookies + rate-limited login. Single admin user (V1).

- Backend: bcrypt password hash in SQLite `admin_settings` table, HMAC-SHA256 session tokens (24h expiry), HttpOnly + SameSite=Strict cookies
- Frontend: Setup page on first visit, login page, logout button in nav
- Rate limiting: 5 login attempts per minute per IP
- Audit log: all admin actions (login, logout, service start/stop/restart, config changes, backups) logged to SQLite
- API endpoints: `/api/truffels/auth/{status,login,setup,logout}`, `/api/truffels/audit`
- New dependency: `golang.org/x/crypto` (bcrypt), Go 1.24 builder image

### 17.2 Secrets cleanup

- Moved hardcoded `CORE_RPC_PASSWORD` and `DATABASE_PASSWORD` from mempool compose into `/srv/truffels/secrets/mempool-backend.env`
- Secrets directory permissions: `root:root 700`, individual files `600`
- No credentials remain in compose files

### 17.3 CORS lockdown

- Removed wildcard `*` CORS middleware entirely — frontend is same-origin via Caddy

### 17.4 Security headers (Caddy)

Added `Permissions-Policy` header. Existing: `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, `-Server`.

### 17.5 Docker container hardening

All containers now have:

| Service | `cap_drop: ALL` | `cap_add` | `security_opt: no-new-privileges` |
|---------|:---:|---|:---:|
| bitcoind | Yes | — | Yes |
| electrs | Yes | — | Yes |
| ckpool | Yes | — | Yes |
| mempool-backend | Yes | — | Yes |
| mempool-frontend | Yes | — | Yes |
| mempool-db (MariaDB) | Yes | CHOWN, DAC_OVERRIDE, FOWNER, SETGID, SETUID | — |
| ckstats | Yes | — | Yes |
| ckstats-cron | Yes | — | Yes |
| ckstats-db (PostgreSQL) | Yes | CHOWN, DAC_OVERRIDE, FOWNER, SETGID, SETUID | — |
| proxy (Caddy) | Yes | CHOWN, DAC_OVERRIDE, FOWNER, NET_BIND_SERVICE, SETGID, SETUID | — |
| truffels-agent | — | — | Yes |
| truffels-api | Yes | — | Yes |
| truffels-web (nginx) | Yes | CHOWN, DAC_OVERRIDE, FOWNER, NET_BIND_SERVICE, SETGID, SETUID | — |

Notes:
- Database containers need CHOWN/FOWNER/SETGID/SETUID for entrypoint user switching (gosu)
- truffels-agent has Docker socket access, so cap_drop not applied (would break Docker CLI)
- truffels-api no longer mounts Docker socket — all Docker ops go through agent
- Containers that bind privileged ports (Caddy:80, nginx:8080 internal) need NET_BIND_SERVICE

### 17.6 Host firewall (nftables)

```bash
# Applied as separate inet table (doesn't conflict with Docker's iptables-nft chains)
sudo nft -f /srv/truffels/config/nftables/truffels.conf
```

Policy: INPUT drop, allow only:
- SSH (22), HTTP (80), Bitcoin P2P (8333), Stratum (3333)
- Loopback, established/related, ICMP
- Docker bridge interfaces (container → host)

Persistence: systemd service `truffels-firewall.service` (After=docker.service).

```bash
sudo systemctl enable truffels-firewall.service
```

### 17.7 Log rotation

Already configured in `/etc/docker/daemon.json`: `local` driver, `max-size: 10m`, `max-file: 5`.

### 17.8 Backup system

API endpoints for backup management:
- `POST /api/truffels/backup/export` — creates tarball of configs, compose files, SQLite DB
- `GET /api/truffels/backup/list` — list existing backups
- `GET /api/truffels/backup/download?filename=...` — download a backup

Backups stored in `/srv/truffels/backups/`, auto-pruned to keep last 5.

Optional `?include_secrets=true` parameter to include `/srv/truffels/secrets/` in backup.

Restore is manual for V1: `tar xzf` into `/srv/truffels/`.

---

## 18. truffels-agent (privileged Docker mediator)

Completed: 2026-03-10, updated 2026-03-13

### 18.1 Purpose

The truffels-agent is the only container with Docker socket access. It mediates all privileged Docker operations (compose up/down/stop/restart, container inspect, log retrieval, system power) on behalf of truffels-api. The API no longer mounts the Docker socket directly.

### 18.2 Image

Custom build — Go 1.24 multi-stage, runtime on `debian:bookworm-slim` with Docker CLI + Compose plugin.

Source: `/home/truffel/Project-Truffels/truffels-agent/`

### 18.3 Security model

- **Allowlisted services only:** 11 services (bitcoind, electrs, ckpool, mempool, mempool-db, ckstats, ckstats-db, proxy, truffels-agent, truffels-api, truffels-web) — all others rejected with 403
- **Allowlisted containers only:** 13 named containers for inspect — unlisted containers get `denied` status
- **No shell access:** All operations use specific `docker compose` / `docker inspect` commands
- **Timeouts:** Compose operations 2 min, inspect 5s, logs 10s
- **Log tail capped:** Max 1000 lines per request
- **System power:** `nsenter -t 1 -m` for host reboot/shutdown (requires `pid: "host"` + `SYS_ADMIN` + `SYS_PTRACE`)

### 18.4 Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | `/v1/compose/up` | Start a service stack |
| POST | `/v1/compose/down` | Remove a service stack |
| POST | `/v1/compose/stop` | Stop a service stack (preserves containers) |
| POST | `/v1/compose/restart` | Restart a service stack |
| POST | `/v1/compose/logs` | Retrieve service logs (tail N) |
| POST | `/v1/compose/build` | Build a service (10 min timeout) |
| POST | `/v1/inspect` | Inspect container states |
| GET | `/v1/stats` | Docker stats for all containers |
| POST | `/v1/image/pull` | Pull a Docker image (10 min timeout) |
| POST | `/v1/image/inspect` | Get image info from a container |
| POST | `/v1/system/restart` | Reboot host via nsenter |
| POST | `/v1/system/shutdown` | Shutdown host via nsenter |
| GET | `/v1/health` | Health check |

### 18.5 Compose file

In `/srv/truffels/compose/truffels/docker-compose.yml` (same compose project as API + web):

- Container: `truffels-agent`
- Port: `9090` (internal only, not exposed to host)
- Network: `truffels-core` (172.22.0.0/24 — API↔agent only, S10.1)
- Volumes: Docker socket (rw), compose directory (ro), config directory (ro), secrets directory (ro)
- `pid: "host"` — required for nsenter to access PID 1 namespaces
- `cap_add: [SYS_ADMIN, SYS_PTRACE]` — SYS_ADMIN for nsenter namespace entry, SYS_PTRACE for `/proc/1/ns/*` access on Linux 6.12+
- No `cap_drop: ALL` — needs Docker socket access
- No `no-new-privileges` — breaks nsenter capability acquisition
- Memory limit: 128M

### 18.6 API integration

truffels-api connects to the agent via `TRUFFELS_AGENT_URL=http://truffels-agent:9090`. The Docker socket mount was removed from the API container. All compose and inspect operations now proxy through the agent's HTTP API. The API is on both `truffels-core` (agent access) and `bitcoin-backend` + `truffels-edge` (service communication).

### 18.7 Docker hardening update

| Service | `cap_drop: ALL` | `cap_add` | `security_opt: no-new-privileges` | Docker socket |
|---------|:---:|---|:---:|:---:|
| truffels-agent | — | SYS_ADMIN, SYS_PTRACE | — | Yes (rw) |
| truffels-api | Yes | — | Yes | No |

### 18.8 Network isolation (S10.1)

The `truffels-core` network (172.22.0.0/24) isolates API↔agent communication:

```bash
docker network create --driver bridge --subnet 172.22.0.0/24 truffels-core
```

Only the agent and API are on this network. No other services can reach the agent's port 9090.

---

## 19. CI pipeline (GitHub Actions)

Completed: 2026-03-10, updated 2026-03-13

### 19.1 Workflow

File: `.github/workflows/ci.yml`

Triggers on push to `main` and pull requests to `main`. Three parallel jobs:

| Job | Runtime | Tests |
|-----|---------|-------|
| API (Go) | `go test -v -count=1 -coverprofile=coverage.out ./...` + golangci-lint (errcheck) | 269 tests across 9 packages |
| Agent (Go) | `go test -v -count=1 -coverprofile=coverage.out ./...` + golangci-lint (errcheck) | 49 tests |
| Web (React) | `pnpm tsc --noEmit` + `pnpm test` (vitest) + `pnpm build` | 49 tests |

Total: 367 tests. Coverage artifacts uploaded via `actions/upload-artifact@v4`.

### 19.2 Test coverage highlights

- **Store:** SQLite CRUD — settings, audit log, services, config revisions, alerts, update checks/logs
- **Auth:** Password hashing, session tokens (create/validate/tamper/expire), setup state
- **Registry:** Service graph, topological sort, dependency validation, compose dir mapping
- **Handlers:** Health, auth flow (setup/login/logout/rate-limit), middleware, state derivation, system power (restart/shutdown with password confirmation)
- **Services:** Full action flow (start/stop/restart) with mock agent, dependency enforcement, config CRUD, stats, alerts, enable/disable, admission control (temp/disk), pull-restart
- **Bitcoin RPC:** Mock RPC server, auth verification, all info endpoints
- **Docker:** Compose client (up/down/stop/restart/logs), container inspection with agent fallback, system actions
- **Metrics:** CPU, memory, temperature, uptime with mock /proc and /sys files
- **Alerts:** Disk/temp thresholds, boundary values, engine lifecycle
- **Updates:** Store CRUD (upsert dedup, pending count), version extraction (Docker Hub/GitHub/Bitbucket), compose file rewriting (tag update, digest strip, rollback), preflight checks (all 7), rollback (9 scenarios), all API endpoints, auth enforcement
- **Agent:** Health, allowlists, compose dir mapping, input validation, inspect, system restart/shutdown, image pull/inspect, compose build, stats endpoint
- **Web formatters:** Uptime, bytes, difficulty, large numbers, hashrate

### 19.3 Running tests locally

Go tests require Docker (multi-stage build, Go not on host):

```bash
# API tests
docker run --rm -v /home/truffel/Project-Truffels/truffels-api:/work -w /work golang:1.24-bookworm go test -v -count=1 ./...

# Agent tests
docker run --rm -v /home/truffel/Project-Truffels/truffels-agent:/work -w /work golang:1.24-bookworm go test -v -count=1 ./...
```

Web tests require Node.js/pnpm (runs in CI only, not on host):

```bash
cd truffels-web && pnpm install && pnpm test
```

---

## 20. Update system

Completed: 2026-03-10

### 20.1 Overview

Automatic update checking for all managed services. Three source types:

| Source | Method | Services |
|--------|--------|----------|
| Docker Hub | Tag comparison (latest stable, filters dev/rc/alpha/beta) | bitcoind, electrs, mempool, caddy, postgres |
| Docker Digest | Remote manifest digest comparison (floating tags) | mempool-db (mariadb:lts) |
| GitHub | Commit SHA on branch | ckstats |
| Bitbucket | Commit SHA on branch | ckpool |
| GitHub Releases | Latest release tag_name | truffels-agent, truffels-api, truffels-web |

Background engine checks every 24 hours. Manual check via UI or API.

### 20.2 Update flow (spec S13.4)

1. **Check:** Query upstream for latest version, compare with running container
2. **Preflight:** Run 7 checks before allowing update (see 20.6)
3. **Confirm:** Operator reviews preflight results in confirmation dialog
4. **Snapshot:** Compose file saved to config revision history
5. **Pull/Build:** Pull new image tags (Docker Hub) or rebuild from source (GitHub/Bitbucket)
6. **Rewrite:** Update compose file image tags (strip digest pins)
7. **Restart:** Compose down → up
8. **Health check:** Wait 30s, verify all containers running and healthy
9. **Rollback:** If unhealthy, revert compose file, pull old images, restart with previous version

Multi-container services (e.g. mempool with backend + frontend) pull all images before restart.

### 20.2b Self-update flow (truffels services)

The agent, API, and web share a single compose stack and version. When a GitHub Release is detected:

1. **Git checkout:** Agent fetches tags and checks out the release tag in `/repo` (mounted from host)
2. **Build:** Agent runs `docker compose build --no-cache --build-arg VERSION=<tag>` for the truffels stack
3. **Rewrite:** API updates image tags in compose file for all three services (agent, api, web)
4. **Sibling logs:** Update logs created for all three services
5. **Detached restart:** Agent writes a shell script to host `/tmp/`, executes it via `nsenter` (host PID namespace). The script sleeps 2s then runs `docker compose up -d`. This survives the agent container being replaced.
6. **Reconciliation:** When the new API starts, it finds "restarting" update logs and marks them done (if healthy) or failed (if unhealthy).

The version is baked into Go binaries via `-ldflags "-X main.version=<tag>"` and into the web UI via `VITE_APP_VERSION` env var. Visible in health endpoints and Settings > Info tab.

### 20.3 Database

Migration `003_updates.sql` adds two tables:
- `update_checks` — latest check result per service (version, has_update, error)
- `update_log` — history of applied updates (status, from/to version, rollback info)

### 20.4 Web UI

- **Updates page:** Card-based responsive layout with version info, status badges, source links (clickable icons for Docker Hub / GitHub / Bitbucket), Check Now / Update All buttons, update history
- **Preflight dialog:** Confirmation modal showing version transition, preflight check results (pass/fail/warn), blocks confirm if any check fails
- **Service detail:** Info card shows current version and update status badge
- **Nav badge:** Pending update count in header (polled every 60s)
- **Responsive header:** Hamburger menu on mobile, horizontal nav on desktop

### 20.5 Preflight checks

Endpoint: `GET /api/truffels/updates/preflight/{id}`

| Check | Type | Description |
|-------|------|-------------|
| service_exists | blocking | Service exists and has update source configured |
| update_available | blocking | Update check confirms newer version exists |
| not_updating | blocking | No update already in progress for this service |
| compose_file | blocking | Compose file is accessible and readable |
| disk_space | blocking | At least 2 GB free on `/srv/truffels` |
| dependency_{name} | blocking | Each dependency service is running and healthy |
| dependent_{name} | warning | Running services that depend on this one (informational) |

Config snapshot (compose file) is automatically saved to revision history before the update starts.

### 20.6 Known issues

- Docker Hub API returns dev builds first — uses `page_size=100` and filters `-dev`, `-rc`, `alpha`, `beta` tags
- Commit-based sources (GitHub/Bitbucket) initialize current=latest on first check to avoid false positives
- `cap_drop: ALL` removes `DAC_OVERRIDE` — API container needs `user: "1000:1000"` to write SQLite
- Secrets dir needed `chgrp 1000` + `chmod 640/750` after API user change

---

## 21. Temperature thresholds

All temperature-related UI and alerting uses a single unified threshold table, aligned to the Raspberry Pi 5 active cooler fan curve.

### Why these thresholds

The Pi 5 official active cooler uses a stepped PWM fan curve. The temperature thresholds for the dashboard color coding and alert engine are aligned to this curve so that visual feedback correlates directly with observable fan behavior:

- **Green (< 60°C):** Fan is off or at low speed — normal idle/light load.
- **Orange (60–74°C):** Fan is ramping up to medium/high — sustained load, nothing dangerous.
- **Red (>= 75°C):** Fan is at or near full speed — system is hot, investigate if unexpected.
- **Critical alert (>= 80°C):** Approaching the 85°C throttle point — action required.

### Threshold table

| Temperature | Dashboard Color | Alert | Fan Curve (approx) |
|-------------|----------------|-------|---------------------|
| < 50°C | green | — | Off |
| 50–59°C | green | — | Low (~30%) |
| 60–74°C | orange | — | Medium to high (~50–75%) |
| 75–79°C | red | warning | Full (~100%) |
| >= 80°C | red | critical | Full (throttle at 85°C) |

### Where these thresholds are enforced

- **Dashboard UI:** `DashboardPage.tsx` — text color on temperature display
- **Alert engine:** `alerts/engine.go` `checkTemp()` — warning at 75°C, critical at 80°C (configurable via Settings page)
- **Monitoring chart:** Temperature/Fan chart on `/admin/monitoring` (single Y-axis 0–100, both % scale)
- **Settings page:** `/admin/settings` → Alerts tab — warning and critical thresholds adjustable at runtime

---

## 22. Per-service monitoring and resource metrics

### What is collected

**Host metrics** (every 60s via alert engine):
- CPU%, memory%, temperature, fan RPM/%, disk usage%
- Network I/O: rx/tx bytes per interval (from `/proc/net/dev`, physical interfaces only)
- Disk I/O: read/write bytes per interval + utilization % (from `/proc/diskstats`, NVMe device)

**Per-container metrics** (every 60s via Docker stats → agent):
- CPU%, memory usage/limit (MB)
- Network rx/tx bytes per interval (delta from previous sample)
- Block read/write bytes per interval (delta from previous sample)
- Delta computation uses `clampDelta()` — returns 0 on counter reset (container restart)

### Storage

- SQLite tables: `metric_snapshots` (host), `container_snapshots` (per-container)
- Pruning: snapshots older than 48h deleted every ~50 minutes
- Service events: last 500 kept

### API endpoints

- `GET /api/truffels/monitoring?hours=24` — host metrics, containers, events, alerts
- `GET /api/truffels/services/{id}/monitoring?hours=24` — per-service container snapshots + live cumulative stats

### Frontend

- **Monitoring page** (`/admin/monitoring`) — 6 host-level charts:
  - CPU %, Memory %, Temperature/Fan (single Y-axis 0–100), Disk Usage % (0–100), Network I/O RX/TX (dual-line), Disk I/O Utilization %
- **Monitor tab** on each service detail page (`/admin/services/{id}`) — 4 Recharts area charts:
  - CPU %, Memory (MB), Network I/O RX/TX (dual-line), Block I/O Read/Write (dual-line)
- Multi-container services show one line per container with legend
- Time range selector: 1h / 6h / 24h (default 6h)
- Current Totals table below charts shows live cumulative stats from Docker
- 15s auto-refresh

### Migrations

- `004_monitoring.sql` — metric_snapshots table (includes fan and I/O columns)
- `005_fan_metrics.sql` — no-op placeholder (columns folded into 004)
- `006_container_metrics.sql` — container_snapshots table
- `007_host_io_metrics.sql` — ALTER TABLE additions for existing databases

---

## 23. Settings page and configurable alerting

### Settings page

Available at `/admin/settings` with three tabs:

**Service Handling tab:**
- **Restart loop detection:** configurable N restarts in M minutes triggers critical alert
  - Default: 5 restarts in 10 minutes
  - Max retries before auto-stop (0 = disabled, default)
  - Engine tracks per-container restart timestamps in a sliding window
- **Dependent service handling:** what happens when an upstream dependency is unhealthy
  - `flag_only` (default): show warning alert, keep dependent services running
  - `flag_and_stop`: show warning + automatically stop dependent services

**Alerts tab:**
- Temperature warning threshold (default 75°C)
- Temperature critical threshold (default 80°C)
- Validated: warning must be < critical

**Danger Zone tab:**
- System restart / shutdown (requires admin password confirmation)
- Triggers logout before showing fullscreen overlay with elapsed timer
- Overlay polls `/api/truffels/health` every second after 15s, redirects to `/admin/` when online
- Executes via agent `nsenter -t 1 -m -- /sbin/reboot|shutdown`
- Agent compose requires `pid: "host"` and `cap_add: [SYS_ADMIN, SYS_PTRACE]`
- `no-new-privileges` must NOT be set on agent (breaks nsenter capability acquisition)

### Backend

- Settings stored as key-value pairs in `admin_settings` SQLite table (same table as auth)
- API endpoints: `GET /settings`, `PUT /settings` (partial updates), `POST /system/shutdown`, `POST /system/restart`
- Agent endpoints: `POST /v1/system/shutdown`, `POST /v1/system/restart`
- Alert engine reads thresholds from settings on every evaluation tick (30s)
- Restart loop logic extracted into `evalRestartLoop()` and `recordRestartIncrements()` for testability

### Dependency graph enforcement

```
bitcoind (no upstream)
  ├── electrs → bitcoind
  │     └── mempool → bitcoind + electrs + mempool-db
  └── ckpool → bitcoind
        └── ckstats → ckpool + ckstats-db
```

When upstream is unhealthy (exited/unhealthy/restarting), dependent services get `upstream_unhealthy` warning alert. In `flag_and_stop` mode, dependents are also auto-stopped via compose down.

### Tests

- 12 engine tests: restart loop windowing, threshold, expiry, counter reset, auto-stop lifecycle, custom temp thresholds
- 9 API tests: GET/PUT settings, unknown keys, float/string values, audit logging, auth enforcement

### Verification

```bash
# Check settings defaults
curl -s http://localhost/api/truffels/settings | jq .

# Update a setting
curl -s -X PUT http://localhost/api/truffels/settings \
  -H 'Content-Type: application/json' \
  -d '{"restart_loop_count": 3}'
```

---

## 24. Things explicitly forbidden during installation

Do not do these things:
- do not install `bitcoind`, `electrs`, `mempool`, or `ckpool` directly on the host again
- do not use `docker.io` as the baseline package source
- do not re-enable forced PCIe Gen 3 for V1
- do not expose services publicly by default
- do not rely on pretty boot parameters instead of runtime verification
- do not resync the blockchain from zero unless the restore is proven bad
- do not treat the old PoC layout as the final architecture

---

## 25. Service enable/disable and compose stop fix

Completed: 2026-03-13

### 25.1 Compose stop vs down (TR-002)

`docker compose down` removes all containers in a shared compose stack, cascading to sibling services. Changed to `docker compose stop` for stop and disable actions — this preserves containers as "exited" without removing co-located services.

The agent now has both `/v1/compose/stop` (preserve containers) and `/v1/compose/down` (remove containers).

### 25.2 Same-stack dependency relaxation

Services sharing a `ComposeDir` (e.g. mempool-backend + mempool-db) skip dependency checks on start, since `docker compose up` in the shared directory starts all services in the stack together.

### 25.3 Stale health status fix

Docker retains the last health check result on stopped containers (showing "unhealthy" for exited containers). The agent now only reports health status when `container.State.Status == "running"`.

### 25.4 Disabled service UI

When a service is disabled:
- State badge shows purple "disabled"
- Start/Stop/Restart buttons are hidden
- Only the Enable button is shown

---

## 26. Admission control

Completed: 2026-03-12

Blocks manual service starts when:
- **Disk free** < configurable threshold (default 10 GB)
- **CPU temperature** >= configurable critical threshold (default 80°C)

Does not affect Docker restart policies (only manual starts via UI/API).

Settings: `admission_disk_min_gb` and `temp_critical` in admin_settings table, configurable via Settings page.

---

## 27. Rollback system

Completed: 2026-03-12

Manual rollback to previous version via service detail page or `POST /api/truffels/updates/rollback/{id}`.

Flow:
1. Finds last successful update in `update_log`
2. Pulls old image (from `previous_version` field)
3. Rewrites compose file image tags
4. Restarts service + 30s health check
5. Logs result to `update_log`

Not available for:
- Floating-tag services (pull overwrites old image layers)
- Custom-built services (no old image to pull)
- Services with no previous successful update

---

## 28. Network isolation (S10.1)

Completed: 2026-03-13

### 28.1 truffels-core network

Created `truffels-core` (172.22.0.0/24) for API↔agent communication only:

```bash
docker network create --driver bridge --subnet 172.22.0.0/24 truffels-core
```

### 28.2 Network zone summary

| Network | Subnet | Purpose | Members |
|---------|--------|---------|---------|
| `bitcoin-backend` | 172.20.0.0/24 | Bitcoin service communication | All service containers + API |
| `truffels-edge` | 172.21.0.0/24 | User-facing (reverse proxy) | proxy, API, web |
| `truffels-core` | 172.22.0.0/24 | Internal control plane | API, agent |

The agent is ONLY on `truffels-core` — no other network. Only the API can reach it.

---

## 29. Functional specification (Pflichtenheft)

Completed: 2026-03-13

`docs/Pflichtenheft_V1.md` — 687-line functional specification covering 19 sections, mapping every Lastenheft requirement (from `Project_Truffels_Spec.md`) to concrete implementation details.

---

## 30. Image versions (as of 2026-03-13)

All images are digest-pinned in the install script. Current deployed versions:

| Service | Image | Version |
|---------|-------|---------|
| bitcoind | btcpayserver/bitcoin | 30.2 |
| electrs | getumbrel/electrs | v0.11.0 |
| mempool-backend | mempool/backend | v3.2.1 |
| mempool-frontend | mempool/frontend | v3.2.1 |
| mempool-db | mariadb | lts (floating tag, digest-checked) |
| ckstats-db | postgres | 16.13-alpine |
| proxy | caddy | 2.11.2-alpine |
| ckpool | truffels/ckpool | v1.0.0 (custom build) |
| ckstats | truffels/ckstats | latest (custom build) |
| agent | truffels/agent | v0.2.0 (custom build, self-updating) |
| api | truffels/api | v0.2.0 (custom build, self-updating) |
| web | truffels/web | v0.2.0 (custom build, self-updating) |

---

## 31. Continuing this file

This file is meant to be extended with:
- completed commands that were actually run
- exact compose files
- config file examples
- validation commands and expected output
- rollback commands
- troubleshooting notes

Each future section should prefer:
- exact commands
- explicit expected results
- no hand-wavy prose

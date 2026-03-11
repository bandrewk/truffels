# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Project Truffels is a Bitcoin-first infrastructure appliance for Raspberry Pi 5 (8 GB) with NVMe storage. It provides strict Docker-based lifecycle management for Bitcoin services (Bitcoin Core, electrs, mempool, ckpool) with a web UI and ePaper status display.

**Current state:** Managed service layer, reverse proxy, control plane (API + web UI), and privileged agent deployed. 13 Docker containers running. CI pipeline with 240+ tests. Next milestone: Phase 9 (ePaper display).

## Key Documents

- `Project_Truffels_Spec.md` - Full architectural specification (the authoritative design reference)
- `INSTALLATION.md` - Operational install runbook with exact commands and verification steps
- `MIGRATION.md` - Migration guide from the old PoC system
- `README.md` - Project summary and current status

## Architecture (Target)

Three-tier architecture, all Docker-managed:

1. **Control Plane:** `truffels-api` (Go), `truffels-web` (React/TypeScript/Vite), `truffels-agent` (restricted Docker interface), reverse proxy (Caddy or Traefik)
2. **Managed Service Layer:** Bitcoin Core, electrs, mempool, ckpool — each defined as a service template with dependency enforcement
3. **Display Subsystem:** `truffels-display-renderer` for WeAct 4.2" ePaper via SPI

## Technology Choices

- **Backend:** Go
- **Frontend:** React + TypeScript + Vite, dark-mode-first
- **Control plane persistence:** SQLite (V1)
- **Container runtime:** Docker Engine + Compose plugin (official Docker APT repo, not `docker.io`)
- **Host OS:** Raspberry Pi OS Lite 64-bit
- **Target hardware:** Pi 5, Samsung 990 PRO 2TB NVMe on Geekworm X1001, PCIe Gen 2 (stability baseline)

## Service Dependency Graph

```
Bitcoin Core (no upstream dependency)
  ├── electrs (requires Bitcoin Core healthy + pruning disabled)
  │     └── mempool (requires Bitcoin Core + electrs healthy)
  └── ckpool (requires Bitcoin Core healthy + RPC auth)
```

## Docker Network Zones

- `truffels-edge` — user-facing (reverse proxy, web UI)
- `truffels-core` — internal control plane (API, agent)
- `bitcoin-backend` — Bitcoin service communication
- `display-io` — optional display renderer isolation

## Host Directory Layout

All product data lives under `/srv/truffels/` on NVMe:
```
/srv/truffels/{compose,config,data,logs,backups,secrets,tmp}
/srv/truffels/data/{bitcoin,ckpool,ckpoolstats,ckstats,electrs,mempool}
```

## Critical Design Rules

- All managed services must be Docker-only — no host-installed binaries for product services
- No `latest` tags in production — stable channel uses pinned versions with digest pinning
- The UI must never get unrestricted Docker socket access; `truffels-agent` mediates privileged operations
- Services are not exposed to LAN unless explicitly enabled; no WAN exposure by default
- Every config change creates a revision (ID, timestamp, actor, diff, validation result)
- The ePaper display is a status surface, not a live dashboard — layout is widget-based (400x300 canvas)
- Display subsystem failures must never affect service management
- V1 is Bitcoin-only — no altcoin support

## Host Rules

The host provides only: boot, kernel, networking, Docker, systemd, journald, nftables, SSH, SPI/GPIO. Product logic must not live on the host. Memory cgroups must be enabled before Docker installation.

## Current System State (as of 2026-03-10)

- **Host:** Raspberry Pi 5 8GB, Raspberry Pi OS Lite 64-bit, booting from NVMe
- **Docker:** 29.3.0 (official APT repo), daemon configured with live-restore and local log driver
- **Cgroups:** v2 with memory controller active
- **Directory layout:** `/srv/truffels/` created per spec
- **Networks:** `bitcoin-backend` (172.20.0.0/24), `truffels-edge` (172.21.0.0/24)
- **Running containers (13):**
  - `truffels-bitcoind` — Bitcoin Core 29.0 (btcpayserver/bitcoin)
  - `truffels-electrs` — Electrum Rust Server v0.10.10 (getumbrel/electrs)
  - `truffels-ckpool` — ckpool v1.0.0 (custom build)
  - `truffels-mempool-backend` — mempool.space backend v3.2.0
  - `truffels-mempool-frontend` — mempool.space frontend v3.2.0
  - `truffels-mempool-db` — MariaDB LTS
  - `truffels-ckstats` — ckpoolstats Next.js dashboard (custom build)
  - `truffels-ckstats-cron` — ckstats seed/update/cleanup cron
  - `truffels-ckstats-db` — PostgreSQL 16 Alpine
  - `truffels-proxy` — Caddy 2.9 Alpine reverse proxy
  - `truffels-agent` — Go privileged Docker mediator v0.1.0
  - `truffels-api` — Go control plane API v0.1.0
  - `truffels-web` — React/TS/Vite control plane UI v0.1.0 (nginx)
- **LAN ports:** 22 (SSH), 80 (Caddy), 3333 (stratum), 8333 (P2P)
- **Swap:** 4GB NVMe swapfile at `/srv/truffels/swapfile` + 2GB zram (6GB total)
- **Firewall:** nftables INPUT drop policy (truffels-firewall.service), allow SSH/80/3333/8333/loopback/docker bridges
- **Auth:** Admin login required for web UI (bcrypt + HMAC session cookies, 24h expiry)
- **Docker hardening:** All containers have cap_drop: ALL (except agent for Docker socket), security_opt: no-new-privileges where possible
- **Backups:** API endpoint exports configs/compose/SQLite to `/srv/truffels/backups/`, keeps last 5
- **Updates:** Automatic version checking (Docker Hub / GitHub / Bitbucket) for 7 services (bitcoind, electrs, mempool, ckpool, ckstats, proxy/Caddy, ckstats-db/PostgreSQL), tag filter support, preflight checks, one-click apply with automatic rollback, pull & restart for floating-tag services (MariaDB), 24h background check cycle
- **Monitoring:** Resource trends (Recharts), container status table, health timeline, actionable errors — `/admin/monitoring`
- **Services:** 11 registered services (5 managed, 6 read-only infrastructure including DB services)
- **CI:** GitHub Actions — 3 parallel jobs (API Go tests, Agent Go tests, Web Vitest), 240+ tests total
- **Installation progress:** INSTALLATION.md completed through step 20 (update system)
- **Next milestone:** Phase 9 — ePaper display (ping user first)

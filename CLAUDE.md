# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Project Truffels is a Bitcoin-first infrastructure appliance for Raspberry Pi 5 (8 GB) with NVMe storage. It provides strict Docker-based lifecycle management for Bitcoin services (Bitcoin Core, electrs, mempool, ckpool) with a web UI and ePaper status display.

**Current state:** Pre-implementation (spec/planning phase). No application source code yet. The destination Raspberry Pi 5 host is live and prepared through INSTALLATION.md step 7.3 (data restore complete). Next step is the first Docker container: bitcoind.

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
/srv/truffels/data/{bitcoin,ckpool,electrs,mempool}
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

## Current System State (as of 2026-03-09)

- **Host:** Raspberry Pi 5 8GB, Raspberry Pi OS Lite 64-bit, booting from NVMe
- **Docker:** 29.3.0 (official APT repo), daemon configured with live-restore and local log driver
- **Cgroups:** v2 with memory controller active
- **Directory layout:** `/srv/truffels/` created per spec
- **Restored data:** Bitcoin (~843GB), electrs (~57GB), ckpool (~1MB), ckpoolstats present
- **Containers:** None running yet (only hello-world test)
- **Installation progress:** INSTALLATION.md completed through step 7.3
- **Next milestone:** Step 9 — bitcoind first-container compose stack

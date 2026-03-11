# Project Truffels

Project Truffels is a Bitcoin-first infrastructure appliance for Raspberry Pi 5.

The goal is not to build a random crypto homelab dashboard. The goal is to build a stable, local-first, Docker-managed appliance with a clean web UI, hardened defaults, and an ePaper status surface.

## Goals

- strict Docker-based lifecycle management for managed services
- high system stability on Raspberry Pi 5 with NVMe storage
- curated service catalog instead of arbitrary compose junk
- modern responsive web UI, desktop-first but usable on smartphones
- support for Bitcoin Core, electrs, mempool, and ckpool as managed service templates
- ePaper status rendering with configurable layouts and update schedules

## Hardware baseline

- Raspberry Pi 5 8 GB
- official 27 W USB-C PSU
- Samsung 990 PRO 2 TB with heatsink
- Geekworm X1001 PCIe to M.2 NVMe shield (https://wiki.geekworm.com/X1001)
- WeAct 4.2 inch ePaper module (https://github.com/WeActStudio/WeActStudio.EpaperModule)

## Stability rules

- PCIe Gen 2 is the supported stable baseline for V1
- the X1001 may require additional 5V/GND power for SSD stability
- NVMe is the primary system and data medium
- microSD is treated as recovery/install media, not the long-term product runtime target
- no blind `latest` tag usage in production

## Current state

Fully operational Bitcoin infrastructure running 13 Docker containers on Raspberry Pi 5:

- **Bitcoin Core 30.2** — full node at chain tip (txindex=1, no pruning)
- **electrs v0.11.0** — Electrum server
- **ckpool v1.0.0** — solo mining pool (stratum on port 3333)
- **mempool.space v3.2.1** — block explorer (frontend + backend + MariaDB)
- **ckstats** — mining stats dashboard (Next.js + PostgreSQL + cron)
- **Caddy 2.11.2** — reverse proxy (HTTP on port 80)
- **truffels-agent** — privileged Docker mediator (Docker socket access)
- **truffels-api** — Go control plane backend (REST API, alerts, metrics)
- **truffels-web** — React admin UI (dark-mode, Tailwind)

Updates: automatic version checking (Docker Hub, GitHub, Bitbucket) for all services including Caddy and PostgreSQL, one-click updates with preflight checks and automatic rollback on health failure. Pull & Restart for floating-tag services (MariaDB).

Monitoring: host resource trends (CPU, memory, temperature/fan, disk usage, network RX/TX, disk I/O utilization), container status table, health timeline, actionable errors. Per-service Monitor tab with CPU, memory, network I/O, and block I/O dual-line charts per container plus live totals.

Security: admin auth (bcrypt + HMAC sessions), nftables firewall, Docker capability hardening, secrets isolation.

CI: GitHub Actions with 310+ tests across Go and TypeScript.

Next milestone: Phase 9 — ePaper display subsystem.

## Documentation

- `Project_Truffels_Spec.md` — architectural specification
- `INSTALLATION.md` — operational install runbook with exact commands
- `MIGRATION.md` — migration guide from the PoC system

## Project position

Truffels should become:

> a stable, local-first, Bitcoin infrastructure appliance for Raspberry Pi, with strong operational safety, curated service management, and a purposeful status display.

It should not become:

> a fragile snowflake server full of manual host hacks that only its creator understands.

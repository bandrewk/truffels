# Truffels

[![CI](https://github.com/bandrewk/truffels/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/bandrewk/truffels/actions/workflows/ci.yml)
[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![GitHub release](https://img.shields.io/github/v/release/bandrewk/truffels?include_prereleases)](https://github.com/bandrewk/truffels/releases)

A self-hosted Bitcoin infrastructure appliance for Raspberry Pi 5. Docker-managed, web-controlled, built for stability.

## What it does

Truffels turns a Raspberry Pi 5 into a fully operational Bitcoin node with mining, block exploration, and a management UI — all running in Docker containers with hardened defaults.

**Services:**

- **Bitcoin Core** — full node or pruned
- **electrs** — Electrum server
- **mempool.space** — block explorer
- **ckpool** — solo mining pool (stratum)
- **ckstats** — mining stats dashboard

**Control plane:**

- **Web UI** — service management, monitoring, alerts, updates, settings
- **REST API** — Go backend with SQLite
- **Agent** — privileged Docker mediator (no direct socket access from UI)
- **Caddy** — reverse proxy

## Demo

![Truffels Demo](docs/screenshots/demo.gif)

## Features

- **One-script installer** — `sudo ./install.sh` handles everything
- **NVMe and SD card support** — auto-detects storage, adapts accordingly
- **Service lifecycle** — start, stop, restart, enable/disable with dependency enforcement
- **Update system** — automatic version checking, one-click updates, rollback on failure
- **Monitoring** — CPU, memory, temperature, disk, network I/O with 48h history
- **Alerts** — restart loop detection, dependency health, disk/temp thresholds
- **Security** — admin auth, nftables firewall, Docker capability hardening, secrets isolation
- **CI** — 450+ tests across Go and TypeScript

## Hardware

| Component | Tested with                                   |
| --------- | --------------------------------------------- |
| Board     | Raspberry Pi 5 (8GB)                          |
| Storage   | Samsung 990 PRO 2TB NVMe (via Geekworm X1001) |
| PSU       | Official 27W USB-C                            |
| OS        | Raspberry Pi OS Lite 64-bit                   |

SD card boot is supported for pruned nodes or testing. NVMe recommended for full nodes.

## Quick Start

```bash
# Prerequisites: Pi 5, Pi OS Lite 64-bit, memory cgroups enabled
git clone https://github.com/bandrewk/truffels.git
cd truffels
sudo ./install.sh
```

The installer will prompt for pruning mode and mining address. For non-interactive installs:

```bash
sudo TRUFFELS_PRUNE_SIZE=0 TRUFFELS_MINING_ADDRESS=<your-btc-address> ./install.sh
```

See [INSTALLATION.md](INSTALLATION.md) for the full runbook.

## Architecture

```
    LAN :80                                                  LAN :3333   LAN :50001
       │                                                         │           │
┌──────v───────┐  truffels-edge   ┌──────────────┐               │           │
│ Caddy (proxy)│─────────────────>│  Web (React) │               │           │
└──────┬───────┘                  └──────────────┘               │           │
       │                                                         │           │
       │  ┌──────────────┐  truffels-core ┌─────────────────┐    │           │
       ├─>│   API (Go)   │───────────────>│  Agent (Go)     │    │           │
       │  └──────────────┘                │  Docker socket  │    │           │
       │                                  └─────────────────┘    │           │
       │                                                         │           │
       │  ══════════════ bitcoin-backend network ══════════════  │           │
       │                                                         │           │
       │  ┌──────────┐   ┌──────────┐   ┌──────────┐             │           │
       ├─>│ mempool  │──>│ mempool  │──>│ MariaDB  │             │           │
       │  │ frontend │   │ backend  │   └──────────┘             │           │
       │  └──────────┘   └────┬─────┘                            │           │
       │                      │                                  │           │
       │  ┌──────────┐   ┌────v─────────────────────────┐        │           │
       ├─>│ ckstats  │   │          bitcoind            │        │           │
       │  └────┬─────┘   └───┬──────────────────────┬───┘        │           │
       │  ┌────v─────┐       │                      │            │           │
       │  │PostgreSQL│       │           ┌──────────v─┐          │           │
       │  └──────────┘       │           │   ckpool   │<─────────│           │
       │                 ┌───v──────┐    └────────────┘          │           │
       │                 │ electrs  │<───────────────────────────────────────│
       │                 └──────────┘                                        │
```

13 containers across 3 isolated Docker bridge networks. No product logic on the host.

## Contributing

Contributions are welcome. Please open an issue first to discuss what you'd like to change.

This project uses [gitflow](https://nvie.com/posts/a-successful-git-branching-model/): branch from `develop`, PR back to `develop`. The `main` branch is for releases only.

## Support Development

**BTC:** `bc1q5sl35at30wtftl4je7p0pwwxhwtekfe23602tj`

## License

[AGPL-3.0](LICENSE)

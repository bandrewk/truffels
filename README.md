# Project Truffels

Project Truffels is a **Bitcoin-first infrastructure appliance** for Raspberry Pi 5.

It is designed to run a curated set of services on a single host with **strict Docker-based lifecycle management**, strong stability rules, hardened defaults, and a clean web interface.

## Why this exists

Most homelab stacks rot in exactly the same way:

- random host-installed services
- plaintext secrets in config files
- manual builds no one can reproduce
- floating versions called “latest” and then blamed when things break
- no rollback story
- no migration story

Truffels exists to not do that.

## V1 goals

- Raspberry Pi 5 + NVMe baseline
- Bitcoin-first service catalog
- strict Docker management for product services
- secure local-first operation
- responsive web UI with default dark mode
- desktop-first design with proper tablet and smartphone support
- configurable 4.2 inch ePaper status display

## Initial service catalog

- Bitcoin Core
- electrs
- mempool
- ckpool

V1 is **not** a general-purpose altcoin launcher.

## Hardware baseline

- Raspberry Pi 5 8 GB
- active cooling
- official 27 W USB-C power supply
- Geekworm X1001 PCIe to M.2 shield
- Samsung 990 Pro 2 TB NVMe with heatsink
- WeAct 4.2 inch ePaper module

## Stability rules

- stable beats fast
- pinned tested versions beat floating `latest`
- PCIe Gen 2 beats unstable Gen 3 bragging rights
- backup before wipe
- preserve the existing fully synced blockchain data
- the host stays minimal

## Web UI stance

The web UI is:

- dark-mode-first
- modern and simple
- responsive
- desktop-first
- usable on smartphone

It is **not** a free-for-all Docker playground.

## Current project state

The proof of concept demonstrated that the target hardware can run Bitcoin Core and related services.

It also demonstrated exactly why the final product must not be a pile of manual host-installed snowflake services.

The current next step is migration to a clean baseline while preserving the existing blockchain data.

## Documentation

- `Project_Truffels_Spec.md` — full product specification
- `MIGRATION.md` — migration guide from the current proof of concept

## Planned architecture

- minimal Raspberry Pi OS Lite host
- Docker Engine as the only product service runtime
- curated service templates
- local web control plane
- ePaper renderer driven by system and service telemetry
- rollback-aware configuration and updates

## Non-goals for V1

- arbitrary compose uploads from the UI
- uncontrolled third-party container execution
- Kubernetes or other heavy orchestration
- public internet exposure by default
- fake enterprise scope

## License

TBD

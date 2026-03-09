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

The host has been rebuilt successfully and now boots from NVMe.

Verified good state includes:

- hostname `truffels`
- root filesystem on NVMe
- no thermal or undervoltage flags at baseline check time
- no obvious NVMe timeout/reset spam in the inspected log window

Current blocker before Docker setup:

- memory cgroups are disabled because `cgroup_disable=memory` is present in `/boot/firmware/cmdline.txt`
- this must be fixed before Docker installation

## Documentation

- `Project_Truffels_Spec.md` contains the architectural specification
- `MIGRATION.md` contains the migration and restore guidance

## Project position

Truffels should become:

> a stable, local-first, Bitcoin infrastructure appliance for Raspberry Pi, with strong operational safety, curated service management, and a purposeful status display.

It should not become:

> a fragile snowflake server full of manual host hacks that only its creator understands.

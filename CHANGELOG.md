# Changelog

All notable changes to Project Truffels will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.2.1] - 2026-03-15

### Added
- Footer with version, GitHub link, and BTC donate address
- AGPL-3.0 license

### Changed
- Electrs port 50001 exposed to LAN for wallet connectivity
- Simplified INSTALLATION.md, added TROUBLESHOOTING.md
- Demo GIF added to README

## [v0.2.0] - 2026-03-13

### Added
- System versioning: single version for agent/api/web, baked into binaries and Docker images via build args
- Version display in Settings Info tab (web UI)
- Self-update system: GitHub Releases as update source for truffels services
- Agent endpoints: `POST /v1/git/checkout`, `POST /v1/compose/up-detached`
- Build args support in `POST /v1/compose/build`
- Detached restart via host PID namespace (nsenter) for self-updates
- Startup reconciliation for updates stuck in "restarting" status
- CHANGELOG.md

### Changed
- Agent, API, and Web services are now updatable (no longer read-only)
- Agent compose volume includes project repo mount (`/repo`) for git operations
- Dockerfiles accept `VERSION` build arg for ldflags/Vite env injection
- Install script uses `TRUFFELS_VERSION` variable for image tags and build args

## [v0.1.0] - 2026-03-01

### Added
- Bitcoin Core (btcpayserver/bitcoin:30.2) with full node support
- Electrum Rust Server (electrs v0.11.0)
- ckpool solo mining with custom build
- mempool.space (backend + frontend v3.2.1) with MariaDB
- ckpoolstats dashboard with PostgreSQL
- Caddy reverse proxy with security headers
- Go control plane API with SQLite persistence
- React/TypeScript/Vite admin web UI (dark mode)
- Privileged agent for Docker socket mediation
- Admin authentication (bcrypt + HMAC sessions)
- Update system (Docker Hub, Docker Digest, GitHub, Bitbucket sources)
- Monitoring with per-container metrics and Recharts charts
- Settings system (7 tabs: Services, Alerts, Updates, Info, Logs, Tuning, Danger Zone)
- Alerting (restart loop detection, dependency health, disk/temp thresholds)
- Admission control (disk space + CPU temperature gates)
- Backup/restore via API
- nftables firewall
- NVMe swap + zram
- CI pipeline (GitHub Actions, 430 tests)

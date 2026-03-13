# Releasing & Self-Update Guide

## How versioning works

Project Truffels uses a **single system version** for the three control plane services (agent, API, web). They always ship together from the same git tag.

- **Source of truth:** Git tags (semver format: `v0.2.0`, `v1.0.0`)
- **Baked into binaries:** Go ldflags `-X main.version=<tag>`
- **Baked into web UI:** Vite env `VITE_APP_VERSION=<tag>`
- **Docker image labels:** `org.opencontainers.image.version=<tag>`
- **Visible at:** `/api/truffels/health`, agent `/v1/health`, Settings > Info tab

External services (bitcoind, electrs, mempool, ckpool, ckstats) keep their own independent version tracking via Docker Hub / GitHub / Bitbucket.

## Configuration

All configurable via environment variables:

| Variable | Default | Where | Purpose |
|----------|---------|-------|---------|
| `TRUFFELS_VERSION` | `v0.2.0` | install.sh | Image tags and build args |
| `TRUFFELS_GITHUB_REPO` | `bandrewk/Project-Truffels` | API container env | GitHub Releases API target |
| `TRUFFELS_REPO_SRC` | `/home/truffel/Project-Truffels` | install.sh | Host path to git repo (mounted as `/repo` in agent) |

**If you move or rename the GitHub repo**, update `TRUFFELS_GITHUB_REPO` in the API container's environment (either in the compose file or in install.sh). No code changes needed.

## Cutting a release

### 1. Make your changes and verify

```bash
# Ensure CI is green
git push origin main
# Wait for all 3 jobs to pass
```

### 2. Bump the version default in install.sh

```bash
# Edit install.sh — change the default
# TRUFFELS_VERSION="${TRUFFELS_VERSION:-v0.3.0}"
```

### 3. Write a changelog entry

Edit `CHANGELOG.md` — add a new section at the top:

```markdown
## [v0.3.0] - 2026-03-15

### Added
- ...

### Changed
- ...

### Fixed
- ...
```

### 4. Commit and tag

```bash
git add install.sh CHANGELOG.md
git commit -m "Release v0.3.0"
git tag -a v0.3.0 -m "v0.3.0"
git push origin main --tags
```

### 5. Create a GitHub Release

This is **required** — the self-update checker specifically hits the GitHub Releases API, not the tags API.

```bash
gh release create v0.3.0 \
  --title "v0.3.0" \
  --notes "See CHANGELOG.md for details"
```

Or via GitHub web UI: **Releases > Draft a new release > choose the tag > publish**.

## How self-update works

### Detection

Every 24 hours (configurable in Settings > Updates), the API calls:

```
GET https://api.github.com/repos/{TRUFFELS_GITHUB_REPO}/releases/latest
```

It compares the response `tag_name` (e.g. `v0.3.0`) against the running image tag (e.g. `truffels/agent:v0.2.0` extracts `v0.2.0`). If different, an update is flagged.

### Applying

When the operator clicks "Update" on any truffels service (or "Update All"):

1. **Git checkout** — Agent runs `git fetch --tags && git checkout v0.3.0` in `/repo`
2. **Build** — Agent runs `docker compose build --no-cache --build-arg VERSION=v0.3.0` for the truffels stack
3. **Rewrite compose** — API updates image tags in `docker-compose.yml` for all three services
4. **Detached restart** — Agent writes a shell script to the host via `nsenter`, executes it detached:
   ```sh
   sleep 2
   docker compose -f /srv/truffels/compose/truffels/docker-compose.yml up -d --remove-orphans
   ```
   This runs on the host PID namespace and survives the agent container being replaced.
5. **All three containers restart** with new images
6. **Reconciliation** — When the new API starts, it finds update logs stuck in "restarting" status and marks them done (if healthy) or failed (if unhealthy)

### What the operator sees

- Updates page shows "v0.3.0 available" for agent/api/web
- Click update on any one (they share a stack, so one update covers all three)
- Status shows "building..." then "restarting..."
- Page may briefly disconnect as the API restarts
- After refresh: all three show new version, update history shows "done"

## Tag format requirements

Tags **must** match: `v` followed by digits and dots only.

Valid: `v0.2.0`, `v1.0`, `v10.20.30`

Invalid (rejected by agent security validation):
- `latest`, `main` — no `v` prefix
- `0.2.0` — missing `v`
- `v0.3.0-beta` — hyphen not allowed
- `v0.3.0-rc1` — pre-release suffixes not allowed

This is intentional — the tag is passed to `git checkout`, so strict validation prevents injection.

## Rollback

Self-update rollback is **manual** — if something goes wrong:

```bash
# SSH into the Pi
cd /home/truffel/Project-Truffels

# Check out the previous version
git fetch --tags
git checkout v0.2.0

# Rebuild
cd /srv/truffels/compose/truffels
docker compose build --no-cache --build-arg VERSION=v0.2.0

# Update the compose file image tags back
# (or just re-run install.sh with TRUFFELS_VERSION=v0.2.0)

# Restart
docker compose up -d
```

The existing rollback UI button does **not** work for self-built services (it can only pull pre-built images from registries). This is by design — rolling back source-built services requires a git checkout.

## Private repositories

The GitHub Releases API requires no authentication for **public** repos. If you make the repo private:

- The self-update checker will get 404 (no releases found)
- You would need to add a GitHub Personal Access Token as a secret and modify the `checkGitHubRelease()` function to send an `Authorization: Bearer <token>` header
- For now, keep the repo public or disable self-update checks in Settings > Updates

## Fresh install with a specific version

```bash
# Override the version at install time
sudo TRUFFELS_VERSION=v0.3.0 ./install.sh
```

Or to point at a different repo:

```bash
sudo TRUFFELS_VERSION=v0.3.0 \
     TRUFFELS_GITHUB_REPO=myorg/my-truffels \
     TRUFFELS_REPO_SRC=/home/truffel/my-truffels \
     ./install.sh
```

## File reference

| File | What to change |
|------|---------------|
| `install.sh` | `TRUFFELS_VERSION` default, `TRUFFELS_GITHUB_REPO` default, `TRUFFELS_REPO_SRC` default |
| `CHANGELOG.md` | Add entry for each release |
| `truffels-api/internal/config/config.go` | `TRUFFELS_GITHUB_REPO` env var and default |
| `truffels-api/internal/service/templates/infra.go` | Hardcoded fallback repo in templates (only used if env var is empty) |
| `truffels-agent/main.go` | `allowedRepoDir` constant (container-side mount point, rarely needs changing) |

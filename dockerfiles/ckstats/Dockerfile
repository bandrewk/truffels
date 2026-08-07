# ckstats — Next.js mining stats dashboard
FROM node:22-slim AS builder

ENV CI=true
# pnpm 10, not @latest. Upstream declares engines.pnpm ">=10.0.0" and keeps its
# settings in package.json under "pnpm" (overrides, onlyBuiltDependencies).
# pnpm 11 stopped reading that field — it moved to pnpm-workspace.yaml — so the
# approvals for sharp and unrs-resolver were silently dropped and the install
# died with ERR_PNPM_IGNORED_BUILDS. Nothing here changed; pnpm 11 was released.
# The major is pinned rather than an exact version because ">=10.0.0" is the
# contract upstream states, and the settings layout is stable within a major.
RUN corepack enable && corepack prepare pnpm@10 --activate

WORKDIR /app
COPY ckpoolstats/package.json ckpoolstats/pnpm-lock.yaml ckpoolstats/pnpm-workspace.yaml* ./
RUN pnpm install --no-frozen-lockfile || (sleep 10 && pnpm install --no-frozen-lockfile) || (sleep 30 && pnpm install --no-frozen-lockfile) || (sleep 60 && pnpm install --no-frozen-lockfile)
RUN pnpm add dotenv || (sleep 10 && pnpm add dotenv) || (sleep 30 && pnpm add dotenv) || (sleep 60 && pnpm add dotenv)

COPY ckpoolstats/ ./

# Patch: prefix client-side fetch() calls with basePath.
# Next.js basePath only applies to routing/links, not raw fetch().
# Without this, /api/* requests hit mempool via Caddy catch-all.
# Use npx prettier to reformat after patching (avoids ESLint/prettier errors).
RUN sed -i "s|fetch('/api/|fetch('/ckstats/api/|g" components/Header.tsx && \
    sed -i "s|fetch(\`/api/|fetch(\`/ckstats/api/|g" components/UserResetButton.tsx components/PrivacyToggle.tsx && \
    npx prettier --write components/Header.tsx components/UserResetButton.tsx components/PrivacyToggle.tsx

# Patch: serve the whole app under /ckstats.
#
# Caddy routes /ckstats to this container WITHOUT `uri strip_prefix`, so Next.js
# has to own the prefix itself. Without basePath the assets are emitted at
# /_next/... , fall through Caddy's mempool catch-all, and every page renders
# blank.
#
# This lived as a hand edit in the working copy until v0.3.1-dev.26, which meant
# (a) every fresh install shipped a broken ckstats, because install.sh clones a
# pristine upstream tree and nothing ever added the line, and (b) the hand edit
# made the working copy dirty, so `git checkout <commit>` refused and no ckstats
# update could ever apply. The working copy must stay an untouched upstream
# checkout; all Truffels changes belong here, at build time.
#
# The grep guard makes the patch idempotent — it re-runs on every build, and a
# second injection would produce a duplicate key. The anchor `const nextConfig
# = {` is stable across the upstream revisions we support (verified against the
# pre-Next-15 config and against 8f2e7c2f8403, which renamed
# experimental.serverComponentsExternalPackages to serverExternalPackages but
# kept the declaration line). If upstream ever drops that line the build fails
# loudly here rather than silently producing a blank dashboard.
#
# truffels-patch:basePath:begin
RUN if ! grep -q "basePath:" next.config.js; then \
      sed -i "s|const nextConfig = {|const nextConfig = {\n  basePath: '/ckstats',|" next.config.js; \
    fi; \
    grep -q "basePath: '/ckstats'" next.config.js || { \
      echo "FATAL: could not inject basePath into next.config.js (upstream layout changed)" >&2; \
      cat next.config.js >&2; \
      exit 1; \
    }
# truffels-patch:basePath:end

RUN pnpm build

FROM node:22-slim

ENV CI=true
# pnpm 10, not @latest. Upstream declares engines.pnpm ">=10.0.0" and keeps its
# settings in package.json under "pnpm" (overrides, onlyBuiltDependencies).
# pnpm 11 stopped reading that field — it moved to pnpm-workspace.yaml — so the
# approvals for sharp and unrs-resolver were silently dropped and the install
# died with ERR_PNPM_IGNORED_BUILDS. Nothing here changed; pnpm 11 was released.
# The major is pinned rather than an exact version because ">=10.0.0" is the
# contract upstream states, and the settings layout is stable within a major.
RUN corepack enable && corepack prepare pnpm@10 --activate
# retry() wraps apt-get in 4 attempts with 10/30/60s backoff so a transient
# flake on deb.debian.org doesn't kill the whole build. Total worst-case sleep
# budget: 100s before giving up.
RUN set -e; \
    retry() { "$@" && return 0; for s in 10 30 60; do sleep $s; "$@" && return 0; done; return 1; }; \
    retry apt-get update && \
    retry apt-get install -y --no-install-recommends cron postgresql-client && \
    rm -rf /var/lib/apt/lists/*

# Empty default on purpose. Unlike ckpool's, this arg drives no checkout — the
# source is bind-mounted from the working copy, so SOURCE_REF is purely a stamp.
# A default like "unknown" would therefore label the image with a version that
# does not exist, and the engine treats any non-empty label as proof of what is
# running. Empty means "not stated", which is the truth for a build that was not
# told its ref.
ARG SOURCE_REF=
LABEL org.truffels.source-ref=$SOURCE_REF

WORKDIR /app
COPY --from=builder /app/.next ./.next
COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/package.json ./
COPY --from=builder /app/pnpm-workspace.yaml* ./
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

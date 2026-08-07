# ckstats — Next.js mining stats dashboard
FROM node:22-slim AS builder

ENV CI=true
RUN corepack enable && corepack prepare pnpm@latest --activate

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

RUN pnpm build

FROM node:22-slim

ENV CI=true
RUN corepack enable && corepack prepare pnpm@latest --activate
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

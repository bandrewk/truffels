# ckstats-dgb — Next.js mining stats dashboard for the DigiByte pool.
#
# Self-contained variant of dockerfiles/ckstats/Dockerfile: it clones the
# upstream source itself instead of COPYing it from a bind-mounted working copy,
# so it builds under the catalog build flow (context = this dir, no source tree
# supplied). Served under /ckstats-dgb so it never collides with the legacy
# ckstats at /ckstats.
FROM node:22-slim AS builder

ENV CI=true
# git to fetch the source; pnpm 10 (see dockerfiles/ckstats for the pin rationale).
RUN set -e; \
    retry() { "$@" && return 0; for s in 10 30 60; do sleep $s; "$@" && return 0; done; return 1; }; \
    retry apt-get update && \
    retry apt-get install -y --no-install-recommends git ca-certificates && \
    rm -rf /var/lib/apt/lists/*
RUN corepack enable && corepack prepare pnpm@10 --activate

WORKDIR /app
# Full clone (blobs on demand), pinned to a known-good commit. Not shallow: a
# pinned checkout needs the history to reach the commit.
ARG SOURCE_REF=8f2e7c2f8403
RUN git clone --filter=blob:none https://github.com/mrv777/ckstats.git . && \
    git checkout "$SOURCE_REF"

RUN pnpm install --no-frozen-lockfile || (sleep 10 && pnpm install --no-frozen-lockfile) || (sleep 30 && pnpm install --no-frozen-lockfile) || (sleep 60 && pnpm install --no-frozen-lockfile)
RUN pnpm add dotenv || (sleep 10 && pnpm add dotenv) || (sleep 30 && pnpm add dotenv) || (sleep 60 && pnpm add dotenv)

# Prefix client-side fetch() calls with the basePath (Next.js basePath does not
# rewrite raw fetch()). Same reasoning as the legacy image, /ckstats-dgb here.
RUN sed -i "s|fetch('/api/|fetch('/ckstats-dgb/api/|g" components/Header.tsx && \
    sed -i "s|fetch(\`/api/|fetch(\`/ckstats-dgb/api/|g" components/UserResetButton.tsx components/PrivacyToggle.tsx && \
    npx prettier --write components/Header.tsx components/UserResetButton.tsx components/PrivacyToggle.tsx

# Serve the whole app under /ckstats-dgb. Idempotent, fails loudly if the
# upstream config layout changed (see the legacy image for the full rationale).
RUN if ! grep -q "basePath:" next.config.js; then \
      sed -i "s|const nextConfig = {|const nextConfig = {\n  basePath: '/ckstats-dgb',|" next.config.js; \
    fi; \
    grep -q "basePath: '/ckstats-dgb'" next.config.js || { \
      echo "FATAL: could not inject basePath into next.config.js (upstream layout changed)" >&2; \
      cat next.config.js >&2; \
      exit 1; \
    }

RUN pnpm build

FROM node:22-slim

ENV CI=true
RUN corepack enable && corepack prepare pnpm@10 --activate
RUN set -e; \
    retry() { "$@" && return 0; for s in 10 30 60; do sleep $s; "$@" && return 0; done; return 1; }; \
    retry apt-get update && \
    retry apt-get install -y --no-install-recommends cron postgresql-client && \
    rm -rf /var/lib/apt/lists/*

ARG SOURCE_REF=8f2e7c2f8403
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

# Run as the node user (uid 1000). The catalog runs this with cap_drop=ALL, so
# a root process would lose CAP_DAC_OVERRIDE and could not read the pool's
# logs (owned by uid 1000). Running as 1000 matches the pool and reads them
# natively; chown /app so the app can still write its runtime cache.
RUN chown -R node:node /app
USER node

EXPOSE 3000

CMD ["pnpm", "start"]

# ckstats — Next.js mining stats dashboard
FROM node:22-slim AS builder

ENV CI=true
RUN corepack enable && corepack prepare pnpm@latest --activate

WORKDIR /app
COPY ckpoolstats/package.json ckpoolstats/pnpm-lock.yaml ckpoolstats/pnpm-workspace.yaml* ./
RUN pnpm install --no-frozen-lockfile
RUN pnpm add dotenv

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
RUN apt-get update && apt-get install -y --no-install-recommends \
    cron postgresql-client \
    && rm -rf /var/lib/apt/lists/*

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

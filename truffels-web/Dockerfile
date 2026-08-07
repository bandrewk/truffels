# truffels-web — React control plane UI
FROM node:22-slim AS builder

# Pin the major. This build works on pnpm 11 today, but @latest means every
# build rolls whatever version shipped that morning — the same unpinned
# dependency that broke the ckstats build when pnpm 11 dropped the package.json
# settings layout. CLAUDE.md forbids latest tags for images; the package manager
# that runs the build deserves the same treatment.
RUN corepack enable && corepack prepare pnpm@11 --activate

WORKDIR /app
COPY package.json pnpm-lock.yaml* pnpm-workspace.yaml* ./
RUN pnpm install --no-frozen-lockfile || (sleep 2 && pnpm install --no-frozen-lockfile) || (sleep 5 && pnpm install --no-frozen-lockfile)
COPY . .
ARG VERSION=dev
ENV VITE_APP_VERSION=${VERSION}
RUN pnpm build

FROM nginx:alpine
ARG VERSION=dev
LABEL org.opencontainers.image.version="${VERSION}"
COPY --from=builder /app/dist /usr/share/nginx/html/admin
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 8080

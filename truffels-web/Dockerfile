# truffels-web — React control plane UI
FROM node:20-slim AS builder

RUN corepack enable && corepack prepare pnpm@latest --activate

WORKDIR /app
COPY package.json pnpm-lock.yaml* ./
RUN pnpm install --no-frozen-lockfile
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

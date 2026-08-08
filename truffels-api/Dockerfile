# truffels-api — Go control plane backend
FROM golang:1.26-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download || (sleep 10 && go mod download) || (sleep 30 && go mod download) || (sleep 60 && go mod download)
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o /truffels-api .

FROM debian:bookworm-slim
ARG VERSION=dev
LABEL org.opencontainers.image.version="${VERSION}"
# retry() wraps a command in 4 attempts with 10/30/60s backoff so a transient
# flake on deb.debian.org doesn't kill the whole build. Total worst-case sleep
# budget: 100s before giving up.
RUN set -e; \
    retry() { "$@" && return 0; for s in 10 30 60; do sleep $s; "$@" && return 0; done; return 1; }; \
    retry apt-get update && \
    retry apt-get install -y --no-install-recommends ca-certificates wget && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /truffels-api /usr/local/bin/truffels-api

RUN mkdir -p /data && chown 1000:1000 /data

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/truffels-api"]

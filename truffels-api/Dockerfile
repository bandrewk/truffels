# truffels-api — Go control plane backend
FROM golang:1.24-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download || (sleep 2 && go mod download) || (sleep 5 && go mod download)
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o /truffels-api .

FROM debian:bookworm-slim
ARG VERSION=dev
LABEL org.opencontainers.image.version="${VERSION}"
# retry() wraps a command in 3 attempts with backoff so a transient flake on
# deb.debian.org doesn't kill the whole build.
RUN set -e; \
    retry() { for i in 1 2 3; do "$@" && return 0; sleep $((i*2)); done; return 1; }; \
    retry apt-get update && \
    retry apt-get install -y --no-install-recommends ca-certificates wget && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /truffels-api /usr/local/bin/truffels-api

RUN mkdir -p /data && chown 1000:1000 /data

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/truffels-api"]

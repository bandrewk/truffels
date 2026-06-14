FROM golang:1.24-bookworm AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download || (sleep 10 && go mod download) || (sleep 30 && go mod download) || (sleep 60 && go mod download)
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o /truffels-agent .

FROM debian:bookworm-slim
ARG VERSION=dev
LABEL org.opencontainers.image.version="${VERSION}"
# retry() wraps a command in 4 attempts with 10/30/60s backoff so a transient
# flake on deb.debian.org or download.docker.com doesn't kill the whole build.
# Inline because Dockerfile RUN steps don't share shell state across layers.
# Total worst-case sleep budget: 100s before giving up — long enough to ride
# through most CDN hiccups without making a happy-path build feel slow.
RUN set -e; \
    retry() { "$@" && return 0; for s in 10 30 60; do sleep $s; "$@" && return 0; done; return 1; }; \
    retry apt-get update && \
    retry apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg wget git && \
    install -m 0755 -d /etc/apt/keyrings && \
    curl -fsSL --retry 4 --retry-delay 10 --retry-max-time 120 \
        https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc && \
    chmod a+r /etc/apt/keyrings/docker.asc && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable" \
       > /etc/apt/sources.list.d/docker.list && \
    retry apt-get update && \
    retry apt-get install -y --no-install-recommends docker-ce-cli docker-compose-plugin && \
    rm -rf /var/lib/apt/lists/*
RUN git config --global --add safe.directory /repo
COPY --from=builder /truffels-agent /usr/local/bin/truffels-agent
ENTRYPOINT ["truffels-agent"]

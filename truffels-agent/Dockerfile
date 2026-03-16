FROM golang:1.24-bookworm AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download || (sleep 2 && go mod download) || (sleep 5 && go mod download)
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o /truffels-agent .

FROM debian:bookworm-slim
ARG VERSION=dev
LABEL org.opencontainers.image.version="${VERSION}"
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl gnupg wget git \
    && install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \
    && chmod a+r /etc/apt/keyrings/docker.asc \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable" \
       > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends docker-ce-cli docker-compose-plugin \
    && rm -rf /var/lib/apt/lists/*
RUN git config --global --add safe.directory /repo
COPY --from=builder /truffels-agent /usr/local/bin/truffels-agent
ENTRYPOINT ["truffels-agent"]

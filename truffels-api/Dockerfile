# truffels-api — Go control plane backend
FROM golang:1.23-bookworm AS builder

WORKDIR /src
COPY . .
RUN go mod tidy && go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /truffels-api .

FROM debian:bookworm-slim

# Install Docker CLI + Compose plugin for service lifecycle management
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl wget && \
    install -m 0755 -d /etc/apt/keyrings && \
    curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc && \
    chmod a+r /etc/apt/keyrings/docker.asc && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] \
    https://download.docker.com/linux/debian bookworm stable" > /etc/apt/sources.list.d/docker.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends docker-ce-cli docker-compose-plugin && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /truffels-api /usr/local/bin/truffels-api

RUN mkdir -p /data && chown 1000:1000 /data

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/truffels-api"]

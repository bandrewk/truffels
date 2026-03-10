# truffels-api — Go control plane backend
FROM golang:1.24-bookworm AS builder

WORKDIR /src
COPY . .
RUN go mod tidy && go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /truffels-api .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates wget && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /truffels-api /usr/local/bin/truffels-api

RUN mkdir -p /data && chown 1000:1000 /data

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/truffels-api"]

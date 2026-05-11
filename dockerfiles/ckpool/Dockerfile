# ckpool v1.0.0 — multi-stage build for arm64
FROM debian:bookworm-slim AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    git build-essential autoconf automake libtool pkg-config \
    libczmq-dev ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build
RUN git clone --depth 1 --branch v1.0.0 https://bitbucket.org/ckolivas/ckpool.git .
RUN ./autogen.sh && ./configure && make -j$(nproc)

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    libczmq4 \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -g 1000 ckpool \
    && useradd -u 1000 -g ckpool -s /usr/sbin/nologin ckpool

COPY --from=builder /build/src/ckpool /usr/local/bin/ckpool
COPY --from=builder /build/src/ckpmsg /usr/local/bin/ckpmsg
COPY --from=builder /build/src/notifier /usr/local/bin/notifier

USER ckpool
ENTRYPOINT ["ckpool"]

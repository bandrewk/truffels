# ckpool v1.0.0 — multi-stage build for arm64
FROM debian:bookworm-slim AS builder

# retry() wraps a command in 4 attempts with 10/30/60s backoff so a transient
# flake on deb.debian.org or bitbucket.org doesn't kill the whole build.
RUN set -e; \
    retry() { "$@" && return 0; for s in 10 30 60; do sleep $s; "$@" && return 0; done; return 1; }; \
    retry apt-get update && \
    retry apt-get install -y --no-install-recommends \
        git build-essential autoconf automake libtool pkg-config \
        libczmq-dev ca-certificates && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /build
# git clone retry — wipe any partial state between attempts so the next clone
# can write into the empty working tree. 4 attempts with 10/30/60s backoff.
RUN set -e; \
    attempt=1; \
    for delay in 0 10 30 60; do \
        [ $delay -gt 0 ] && sleep $delay; \
        rm -rf .git ./* 2>/dev/null || true; \
        if git clone --depth 1 --branch v1.0.0 https://bitbucket.org/ckolivas/ckpool.git .; then \
            break; \
        fi; \
        attempt=$((attempt+1)); \
        [ $attempt -gt 4 ] && exit 1; \
    done
RUN ./autogen.sh && ./configure && make -j$(nproc)

FROM debian:bookworm-slim
RUN set -e; \
    retry() { "$@" && return 0; for s in 10 30 60; do sleep $s; "$@" && return 0; done; return 1; }; \
    retry apt-get update && \
    retry apt-get install -y --no-install-recommends libczmq4 && \
    rm -rf /var/lib/apt/lists/* && \
    groupadd -g 1000 ckpool && \
    useradd -u 1000 -g ckpool -s /usr/sbin/nologin ckpool

COPY --from=builder /build/src/ckpool /usr/local/bin/ckpool
COPY --from=builder /build/src/ckpmsg /usr/local/bin/ckpmsg
COPY --from=builder /build/src/notifier /usr/local/bin/notifier

USER ckpool
ENTRYPOINT ["ckpool"]

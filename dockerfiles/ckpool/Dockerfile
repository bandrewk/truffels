# ckpool v1.0.0 — multi-stage build for arm64
FROM debian:bookworm-slim AS builder

# retry() wraps a command in 3 attempts with backoff so a transient flake on
# deb.debian.org or bitbucket.org doesn't kill the whole build.
RUN set -e; \
    retry() { for i in 1 2 3; do "$@" && return 0; sleep $((i*2)); done; return 1; }; \
    retry apt-get update && \
    retry apt-get install -y --no-install-recommends \
        git build-essential autoconf automake libtool pkg-config \
        libczmq-dev ca-certificates && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /build
# git clone retry — wipe any partial state between attempts so the next clone
# can write into the empty working tree.
RUN set -e; \
    for i in 1 2 3; do \
        rm -rf .git ./* 2>/dev/null || true; \
        git clone --depth 1 --branch v1.0.0 https://bitbucket.org/ckolivas/ckpool.git . && break; \
        [ $i -eq 3 ] && exit 1; \
        sleep $((i*2)); \
    done
RUN ./autogen.sh && ./configure && make -j$(nproc)

FROM debian:bookworm-slim
RUN set -e; \
    retry() { for i in 1 2 3; do "$@" && return 0; sleep $((i*2)); done; return 1; }; \
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

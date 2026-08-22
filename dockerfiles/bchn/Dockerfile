# Bitcoin Cash Node (BCHN) — built from official release binaries.
#
# There is no official BCHN Docker image. BCHN publishes signed release
# tarballs at download.bitcoincashnode.org; we pin the exact aarch64 tarball
# by SHA256.
#
# BCHN only serves the current release under /releases/latest/ (no versioned
# directory — verified 2026-08-22, the versioned path 404s). The SHA256 pin
# below therefore doubles as the version guard: if "latest" ever moves past
# 29.1.0, this build fails loudly at the checksum check instead of silently
# shipping a different node.
#
# The checksum is from the maintainer-signed SHA256SUMS.29.1.0.asc.Calin_Culianu.
# Cross-checked: the x86_64 line in that same signed file matches the value the
# BCH reference build verified via GPG against Calin Culianu's key
# (D465135F97D0047E18E99DC321810A542031C02C).
FROM debian:bookworm-slim AS fetch

ARG BCHN_VERSION=29.1.0
ARG BCHN_SHA256=15c5c0400c828d6690970200ad59c18207313c7047b29d571f7e9870d06300ea

RUN apt-get update && apt-get install -y --no-install-recommends \
        curl ca-certificates && rm -rf /var/lib/apt/lists/*

WORKDIR /tmp
RUN set -eu; \
    url="https://download.bitcoincashnode.org/releases/latest/linux/bitcoin-cash-node-${BCHN_VERSION}-aarch64-linux-gnu.tar.gz"; \
    curl -fsSL -o bchn.tar.gz "$url"; \
    echo "${BCHN_SHA256}  bchn.tar.gz" | sha256sum -c -; \
    tar -xzf bchn.tar.gz; \
    mkdir -p /out; \
    cp "bitcoin-cash-node-${BCHN_VERSION}/bin/bitcoind" "bitcoin-cash-node-${BCHN_VERSION}/bin/bitcoin-cli" /out/

FROM debian:bookworm-slim

# BCHN release binaries link against the usual C/C++ runtime; the slim base
# provides libstdc++6 and friends. No boost/libevent packages needed.
RUN apt-get update && apt-get install -y --no-install-recommends \
        libstdc++6 && rm -rf /var/lib/apt/lists/*

RUN useradd -u 1000 -m -s /bin/bash bch

COPY --from=fetch /out/bitcoind /out/bitcoin-cli /usr/local/bin/

USER 1000:1000
WORKDIR /data
ENTRYPOINT ["bitcoind"]

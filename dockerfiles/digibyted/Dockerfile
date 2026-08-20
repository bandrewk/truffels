# DigiByte Core — built from official release binaries.
#
# There is no official DigiByte Docker image. Docker Hub only offers
# third-party repos with zero stars; for an appliance that pins versions
# and checks digests that is not an acceptable source.
#
# The release ships no SHA256SUMS file. The checksum below was computed
# at download time on 2026-08-20 and pins that state against later silent
# replacement. It does not substitute a signature check.
FROM debian:bookworm-slim AS fetch

ARG DIGIBYTE_VERSION=9.26.5
ARG DIGIBYTE_SHA256=6baf0c2673bcd635620108116ad57a2a5dc8e9e3d9380a2d708c070a72508587

RUN apt-get update && apt-get install -y --no-install-recommends \
        curl ca-certificates && rm -rf /var/lib/apt/lists/*

WORKDIR /tmp
RUN set -eu; \
    url="https://github.com/DigiByte-Core/digibyte/releases/download/v${DIGIBYTE_VERSION}/digibyte-${DIGIBYTE_VERSION}-aarch64-linux-gnu.tar.gz"; \
    curl -fsSL -o dgb.tar.gz "$url"; \
    echo "${DIGIBYTE_SHA256}  dgb.tar.gz" | sha256sum -c -; \
    tar -xzf dgb.tar.gz; \
    mkdir -p /out; \
    cp "digibyte-${DIGIBYTE_VERSION}/bin/digibyted" "digibyte-${DIGIBYTE_VERSION}/bin/digibyte-cli" /out/

FROM debian:bookworm-slim

# The release binaries are effectively statically linked — verified via ldd
# on 2026-08-20: only libpthread, libm, libgcc_s and libc. No boost,
# libevent or ZMQ packages required.
RUN useradd -u 1000 -m -s /bin/bash dgb

COPY --from=fetch /out/digibyted /out/digibyte-cli /usr/local/bin/

USER 1000:1000
WORKDIR /data
ENTRYPOINT ["digibyted"]

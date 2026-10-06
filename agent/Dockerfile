FROM rust:1-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
        protobuf-compiler \
        pkg-config \
        libssl-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

COPY control-plane/proto/ ./control-plane/proto/
COPY agent/ ./agent/

WORKDIR /build/agent

RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/build/agent/target \
    cargo build --release && \
    cp target/release/cami-agent /cami-agent

# ── runtime ──────────────────────────────────────────────────────────────────
FROM debian:bookworm-slim

# Fixed uid/gid: scripts/gen-certs.sh hands this device's key to uid 10001.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -r -g 10001 cami && useradd -r -u 10001 -g cami -s /sbin/nologin cami \
    && mkdir -p /var/lib/cami/models && chown -R cami:cami /var/lib/cami

COPY --from=builder /cami-agent /cami-agent

USER cami:cami

ENTRYPOINT ["/cami-agent"]

FROM rust:1-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
        protobuf-compiler \
        pkg-config \
        libssl-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

COPY control-plane/proto/ ./proto/
COPY agent/ ./agent/

WORKDIR /build/agent

RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/build/agent/target \
    cargo build --release && \
    cp target/release/cami-agent /cami-agent

# ── runtime ──────────────────────────────────────────────────────────────────
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -r cami && useradd -r -g cami -s /sbin/nologin cami

COPY --from=builder /cami-agent /cami-agent

USER cami:cami

ENTRYPOINT ["/cami-agent"]

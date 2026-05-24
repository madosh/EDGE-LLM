FROM golang:1.23-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
        protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*

RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2 \
 && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0

WORKDIR /build/control-plane

COPY control-plane/ .

RUN mkdir -p gen && \
    protoc \
      --go_out=. \
      --go_opt=module=github.com/cami-fleet/control-plane \
      --go-grpc_out=. \
      --go-grpc_opt=module=github.com/cami-fleet/control-plane \
      -I proto \
      proto/device.proto

RUN GOFLAGS=-mod=mod go mod tidy
RUN GOFLAGS=-mod=mod CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server ./cmd/server

# ── runtime ──────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /server /server

USER nonroot:nonroot
EXPOSE 8080 9090

HEALTHCHECK --interval=10s --timeout=3s --retries=5 \
    CMD ["/server", "--health-check"]

ENTRYPOINT ["/server"]

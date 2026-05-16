#!/bin/sh
set -e

CERT_DIR="${CERT_DIR:-/certs}"
mkdir -p "$CERT_DIR"

# Idempotent: skip if already generated
if [ -f "$CERT_DIR/ca.crt" ] && [ -f "$CERT_DIR/server.crt" ] && [ -f "$CERT_DIR/device.crt" ]; then
    echo "Certificates already present — skipping generation"
    exit 0
fi

echo "Generating mTLS certificates..."

# ── Certificate Authority ──────────────────────────────────────────────────────
openssl genrsa -out "$CERT_DIR/ca.key" 4096
openssl req -x509 -new -key "$CERT_DIR/ca.key" \
    -out "$CERT_DIR/ca.crt" \
    -days 3650 -nodes \
    -subj "/CN=cami-fleet-ca/O=CamiFleet/C=XX"

# ── Control-plane server cert ─────────────────────────────────────────────────
openssl genrsa -out "$CERT_DIR/server.key" 4096
openssl req -new -key "$CERT_DIR/server.key" \
    -out "$CERT_DIR/server.csr" \
    -subj "/CN=control-plane/O=CamiFleet/C=XX"

cat > /tmp/server-ext.cnf << 'EOF'
subjectAltName=DNS:control-plane,DNS:localhost,IP:127.0.0.1
EOF

openssl x509 -req -in "$CERT_DIR/server.csr" \
    -CA "$CERT_DIR/ca.crt" -CAkey "$CERT_DIR/ca.key" \
    -CAcreateserial \
    -out "$CERT_DIR/server.crt" \
    -days 3650 \
    -extfile /tmp/server-ext.cnf

# ── Device client cert (shared by simulated agents in v0) ─────────────────────
openssl genrsa -out "$CERT_DIR/device.key" 4096
openssl req -new -key "$CERT_DIR/device.key" \
    -out "$CERT_DIR/device.csr" \
    -subj "/CN=device-agent/O=CamiFleet/C=XX"

openssl x509 -req -in "$CERT_DIR/device.csr" \
    -CA "$CERT_DIR/ca.crt" -CAkey "$CERT_DIR/ca.key" \
    -CAcreateserial \
    -out "$CERT_DIR/device.crt" \
    -days 3650

# Restrict private key permissions
chmod 600 "$CERT_DIR"/*.key

echo "Certificate generation complete:"
ls -la "$CERT_DIR"
echo "Server CN: $(openssl x509 -noout -subject -in $CERT_DIR/server.crt)"
echo "Device CN: $(openssl x509 -noout -subject -in $CERT_DIR/device.crt)"

#!/bin/sh
# Generates the fleet's mTLS material.
#
#   $CA_DIR/                    CA private key — mounted ONLY into this job
#   $CERT_DIR/server/           ca.crt, server.crt, server.key  → control plane
#   $CERT_DIR/devices/<name>/   ca.crt, device.crt, device.key  → that device only
#
# Each device certificate's common name is the device name. The control plane
# reads the caller's identity from that name, so one device's key cannot act
# for another. Idempotent: existing files are kept, and devices added to
# DEVICE_NAMES later get a certificate on the next run.
set -e

CERT_DIR="${CERT_DIR:-/certs}"
CA_DIR="${CA_DIR:-/ca}"
DEVICE_NAMES="${DEVICE_NAMES:-device-barcelona-1 device-barcelona-2}"
DAYS="${CERT_DAYS:-825}"
# Users the services run as: distroless "nonroot" for the control plane, the
# fixed "cami" user from agent/Dockerfile for agents.
SERVER_UID="${SERVER_UID:-65532}"
AGENT_UID="${AGENT_UID:-10001}"
# Extra names devices use to reach the control plane, e.g. a LAN address for a
# physical Raspberry Pi: SERVER_SANS="DNS:fleet.local,IP:192.168.1.20".
# The server certificate is issued once; to change its names, delete
# $CERT_DIR/server and run this script again.
SERVER_SANS="${SERVER_SANS:-}"

mkdir -p "$CERT_DIR" "$CA_DIR"

# ── Old layout: one shared device cert, CA key beside it. Remove it. ──────────
if [ -f "$CERT_DIR/ca.key" ] || [ -f "$CERT_DIR/device.key" ]; then
    echo "Removing legacy shared-certificate layout from $CERT_DIR"
    rm -f "$CERT_DIR"/ca.key "$CERT_DIR"/ca.crt "$CERT_DIR"/ca.srl \
          "$CERT_DIR"/server.* "$CERT_DIR"/device.*
fi

# ── Certificate Authority ─────────────────────────────────────────────────────
if [ ! -f "$CA_DIR/ca.key" ] || [ ! -f "$CA_DIR/ca.crt" ]; then
    echo "Creating fleet CA"
    openssl genrsa -out "$CA_DIR/ca.key" 4096
    openssl req -x509 -new -key "$CA_DIR/ca.key" \
        -out "$CA_DIR/ca.crt" \
        -days 3650 \
        -subj "/CN=cami-fleet-ca/O=CamiFleet/C=XX"
    # Certificates signed by a previous CA are useless now.
    rm -rf "$CERT_DIR/server" "$CERT_DIR/devices"
fi
chmod 600 "$CA_DIR/ca.key"

# sign <dir> <name> <cn> <extfile> <owner-uid>
sign() {
    dir="$1"; name="$2"; cn="$3"; ext="$4"; owner="$5"
    mkdir -p "$dir"
    openssl genrsa -out "$dir/$name.key" 2048
    openssl req -new -key "$dir/$name.key" -out "$dir/$name.csr" \
        -subj "/CN=$cn/O=CamiFleet/C=XX"
    openssl x509 -req -in "$dir/$name.csr" \
        -CA "$CA_DIR/ca.crt" -CAkey "$CA_DIR/ca.key" -CAcreateserial \
        -out "$dir/$name.crt" -days "$DAYS" -extfile "$ext"
    rm -f "$dir/$name.csr"
    cp "$CA_DIR/ca.crt" "$dir/ca.crt"
    # The key belongs to the one service that uses it, readable by nobody else.
    chown -R "$owner:$owner" "$dir"
    chmod 700 "$dir"
    chmod 600 "$dir/$name.key"
    chmod 644 "$dir/$name.crt" "$dir/ca.crt"
}

EXT_DIR="$(mktemp -d)"
trap 'rm -rf "$EXT_DIR"' EXIT

# ── Control-plane server certificate ──────────────────────────────────────────
if [ ! -f "$CERT_DIR/server/server.crt" ]; then
    case "$SERVER_SANS" in
        *[!A-Za-z0-9.:,_-]*) echo "Invalid SERVER_SANS: '$SERVER_SANS'" >&2; exit 1 ;;
    esac
    cat > "$EXT_DIR/server.cnf" << EOF
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:control-plane,DNS:localhost,IP:127.0.0.1${SERVER_SANS:+,$SERVER_SANS}
EOF
    echo "Issuing server certificate"
    sign "$CERT_DIR/server" server control-plane "$EXT_DIR/server.cnf" "$SERVER_UID"
fi

# ── One client certificate per device ─────────────────────────────────────────
cat > "$EXT_DIR/client.cnf" << 'EOF'
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=clientAuth
EOF
for name in $DEVICE_NAMES; do
    case "$name" in
        *[!A-Za-z0-9._-]*|"") echo "Invalid device name: '$name'" >&2; exit 1 ;;
    esac
    if [ ! -f "$CERT_DIR/devices/$name/device.crt" ]; then
        echo "Issuing device certificate for $name"
        sign "$CERT_DIR/devices/$name" device "$name" "$EXT_DIR/client.cnf" "$AGENT_UID"
    fi
done

# Each service also mounts only its own directory (see docker-compose.yml), so
# no container can even see another's key; the CA key never leaves $CA_DIR.

echo "Certificate generation complete."
echo "Server: $(openssl x509 -noout -subject -in "$CERT_DIR/server/server.crt")"
for name in $DEVICE_NAMES; do
    echo "Device: $(openssl x509 -noout -subject -in "$CERT_DIR/devices/$name/device.crt")"
done

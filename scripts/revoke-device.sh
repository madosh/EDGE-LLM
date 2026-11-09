#!/bin/sh
# Revokes (or un-revokes) one device's client certificate.
#
#   scripts/revoke-device.sh NAME          # revoke
#   scripts/revoke-device.sh NAME --undo   # take it off the list again
#
# Appends the certificate's serial to revoked.txt in the control plane's cert
# directory. The control plane re-reads that file on change: new handshakes
# from the device are refused at once, and its open gRPC streams are closed
# within 5 s. It then misses heartbeats and is marked offline after 30 s.
#
# Runs in the cert-gen container, which mounts the certs volume, so the
# volume never has to be exposed on the host.
set -e

NAME="$1"
UNDO=0
[ "$2" = "--undo" ] && UNDO=1
if [ -z "$NAME" ]; then
    echo "usage: $0 DEVICE_NAME [--undo]" >&2
    exit 2
fi

# Git Bash on Windows would rewrite the container paths below.
export MSYS_NO_PATHCONV=1

${COMPOSE:-docker compose} run --rm --no-deps -T -e NAME="$NAME" -e UNDO="$UNDO" cert-gen sh -c '
set -e
apk add -q --no-cache openssl >/dev/null
crt="/certs/devices/$NAME/device.crt"
list=/certs/server/revoked.txt
[ -f "$crt" ] || { echo "no certificate for $NAME" >&2; exit 1; }
serial=$(openssl x509 -noout -serial -in "$crt" | cut -d= -f2)
touch "$list"
chmod 644 "$list"
if [ "$UNDO" = 1 ]; then
    grep -v "^$serial " "$list" > /tmp/revoked || true
    cat /tmp/revoked > "$list"
    echo "un-revoked $NAME (serial $serial)"
elif grep -q "^$serial " "$list"; then
    echo "$NAME (serial $serial) is already revoked"
else
    echo "$serial  # $NAME, revoked $(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$list"
    echo "revoked $NAME (serial $serial)"
fi
'

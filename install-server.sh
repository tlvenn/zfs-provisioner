#!/usr/bin/env bash
set -euo pipefail

# Install or update zfs-provisioner server on the host
#
# Usage:
#   ./install-server.sh                    # install latest release
#   ./install-server.sh v1.0.0             # install specific release
#   LISTEN_ADDR=0.0.0.0:9274 ./install-server.sh  # override listen address

BINARY_NAME="zfs-provisioner"
INSTALL_DIR="/usr/local/bin"
SERVICE_NAME="zfs-provisioner"
REPO="tlvenn/zfs_provisioner"

# Auto-detect listen addresses from Incus bridge networks.
# Binds to each bridge IP specifically rather than 0.0.0.0 for security.
# Without Incus, defaults to localhost only.
detect_listen_addr() {
  if command -v incus &>/dev/null; then
    local bridge_ips
    bridge_ips=$(incus network list --format csv 2>/dev/null \
      | awk -F, '$2=="bridge" && $3=="YES" {print $4}' \
      | cut -d/ -f1 \
      | sed 's/$/:9274/' \
      | paste -sd, -)
    if [ -n "$bridge_ips" ]; then
      echo "$bridge_ips"
      return
    fi
  fi
  echo "127.0.0.1:9274"
}

LISTEN_ADDR="${LISTEN_ADDR:-$(detect_listen_addr)}"

# Parse args
VERSION=""
while [ $# -gt 0 ]; do
  case "$1" in
    v*) VERSION="$1"; shift ;;
    --listen) shift; LISTEN_ADDR="$1"; shift ;;
    --listen=*) LISTEN_ADDR="${1#*=}"; shift ;;
    *) echo "Unknown argument: $1"; exit 1 ;;
  esac
done

# Determine download URL
if [ -n "$VERSION" ]; then
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY_NAME}-linux-amd64"
  echo "Installing ${BINARY_NAME} ${VERSION}..."
else
  URL="https://github.com/${REPO}/releases/latest/download/${BINARY_NAME}-linux-amd64"
  echo "Installing ${BINARY_NAME} (latest release)..."
fi

echo "Listen address: ${LISTEN_ADDR}"

# Download binary
TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT

if ! curl -fsSL -o "$TMP" "$URL"; then
  echo "Download failed. If no release exists yet, build locally:"
  echo "  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags='-s -w' -o ${BINARY_NAME} ./cmd/provisioner"
  echo "  sudo cp ${BINARY_NAME} ${INSTALL_DIR}/"
  exit 1
fi

chmod +x "$TMP"

# Stop service if running
if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
  echo "Stopping ${SERVICE_NAME}..."
  sudo systemctl stop "$SERVICE_NAME"
fi

# Install binary
sudo mv "$TMP" "${INSTALL_DIR}/${BINARY_NAME}"
trap - EXIT
echo "Installed ${INSTALL_DIR}/${BINARY_NAME}"

# Create/update systemd service
sudo tee /etc/systemd/system/${SERVICE_NAME}.service > /dev/null <<EOF
[Unit]
Description=ZFS Provisioner API
After=network.target zfs-mount.target

[Service]
ExecStart=${INSTALL_DIR}/${BINARY_NAME} serve --listen ${LISTEN_ADDR}
Restart=always
RestartSec=5
ProtectSystem=full
ProtectHome=true
NoNewPrivileges=false
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now "$SERVICE_NAME"

echo ""
echo "Service status:"
sudo systemctl status "$SERVICE_NAME" --no-pager -l

echo ""
echo "Health check:"
# Check the first listen address
HEALTH_ADDR=$(echo "$LISTEN_ADDR" | cut -d, -f1)
curl -s "http://${HEALTH_ADDR}/health" && echo "" || echo "Health check failed (ZFS may not be available on this host)"

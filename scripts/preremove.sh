#!/bin/sh
set -e
# Stop the service if running as a systemd unit
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet servregistry 2>/dev/null; then
        echo "Stopping servregistry ..."
        systemctl stop servregistry || true
        systemctl disable servregistry || true
    fi
fi

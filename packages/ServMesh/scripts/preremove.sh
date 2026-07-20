#!/bin/sh
set -e
# Stop the service if running as a systemd unit
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet servmesh 2>/dev/null; then
        echo "Stopping servmesh ..."
        systemctl stop servmesh || true
        systemctl disable servmesh || true
    fi
fi

#!/bin/sh
set -e
# Stop the service if running as a systemd unit
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet servpool 2>/dev/null; then
        echo "Stopping servpool ..."
        systemctl stop servpool || true
        systemctl disable servpool || true
    fi
fi

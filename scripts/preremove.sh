#!/bin/sh
set -e
# Stop the service if running as a systemd unit
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet servqueue 2>/dev/null; then
        echo "Stopping servqueue ..."
        systemctl stop servqueue || true
        systemctl disable servqueue || true
    fi
fi

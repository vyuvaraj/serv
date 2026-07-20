#!/bin/sh
set -e
# Stop the service if running as a systemd unit
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet servauth 2>/dev/null; then
        echo "Stopping servauth ..."
        systemctl stop servauth || true
        systemctl disable servauth || true
    fi
fi

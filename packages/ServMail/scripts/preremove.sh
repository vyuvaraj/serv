#!/bin/sh
set -e
# Stop the service if running as a systemd unit
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet servmail 2>/dev/null; then
        echo "Stopping servmail ..."
        systemctl stop servmail || true
        systemctl disable servmail || true
    fi
fi

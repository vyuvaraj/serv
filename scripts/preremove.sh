#!/bin/sh
set -e
# Stop the service if running as a systemd unit
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet servstore 2>/dev/null; then
        echo "Stopping servstore ..."
        systemctl stop servstore || true
        systemctl disable servstore || true
    fi
fi

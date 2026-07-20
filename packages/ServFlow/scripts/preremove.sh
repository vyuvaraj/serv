#!/bin/sh
set -e
# Stop the service if running as a systemd unit
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet servflow 2>/dev/null; then
        echo "Stopping servflow ..."
        systemctl stop servflow || true
        systemctl disable servflow || true
    fi
fi

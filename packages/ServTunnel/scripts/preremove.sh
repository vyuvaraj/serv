#!/bin/sh
set -e
# Stop the service if running as a systemd unit
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet servtunnel 2>/dev/null; then
        echo "Stopping servtunnel ..."
        systemctl stop servtunnel || true
        systemctl disable servtunnel || true
    fi
fi

#!/bin/sh
set -e
# Stop the service if running as a systemd unit
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet servcron 2>/dev/null; then
        echo "Stopping servcron ..."
        systemctl stop servcron || true
        systemctl disable servcron || true
    fi
fi

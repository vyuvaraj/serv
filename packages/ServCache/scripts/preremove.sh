#!/bin/sh
set -e
# Stop the service if running as a systemd unit
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet servcache 2>/dev/null; then
        echo "Stopping servcache ..."
        systemctl stop servcache || true
        systemctl disable servcache || true
    fi
fi

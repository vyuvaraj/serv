#!/bin/sh
set -e
# Stop the service if running as a systemd unit
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet servconsole 2>/dev/null; then
        echo "Stopping servconsole ..."
        systemctl stop servconsole || true
        systemctl disable servconsole || true
    fi
fi

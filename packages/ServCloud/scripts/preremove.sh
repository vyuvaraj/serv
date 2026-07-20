#!/bin/sh
set -e
# Stop the service if running as a systemd unit
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet servcloud 2>/dev/null; then
        echo "Stopping servcloud ..."
        systemctl stop servcloud || true
        systemctl disable servcloud || true
    fi
fi

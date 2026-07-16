#!/bin/sh
set -e
# Stop the service if running as a systemd unit
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet servtrace 2>/dev/null; then
        echo "Stopping servtrace ..."
        systemctl stop servtrace || true
        systemctl disable servtrace || true
    fi
fi

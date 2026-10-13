#!/bin/sh
set -e

# RPM passes the number of remaining package instances: 0 on erase, 1 or more
# on upgrade. Debian passes "remove" or "upgrade".
case "${1:-}" in
    0 | remove)
        if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
            systemctl stop zpool-status-exporter.service || true
            systemctl disable zpool-status-exporter.service || true
        fi
        ;;
esac

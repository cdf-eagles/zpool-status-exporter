#!/bin/sh
#
# Check the .rpm's signature against the OpenPGP public key, then install
# it, check the installed files and the exporter's version, remove it, and
# install it again. Run as root in an AlmaLinux container.
#
# Usage: check-rpm.sh <version> <rpm> <public-key>

set -eu

if [ "$#" -ne 3 ]; then
    echo "usage: $0 <version> <rpm> <public-key>" >&2
    exit 64
fi

version=$1
rpm=$2
key=$3

rpm --import "${key}"
# rpm --checksig exits 0 for an unsigned package too.
rpm --checksig "${rpm}" | tee /tmp/checksig
grep -q ': digests signatures OK$' /tmp/checksig

dnf install -y --setopt=tsflags= "${rpm}"
for f in /usr/bin/zpool-status-exporter \
    /usr/lib/systemd/system/zpool-status-exporter.service \
    /etc/sysconfig/zpool-status-exporter \
    /usr/share/man/man8/zpool-status-exporter.8.gz; do
    if [ ! -f "${f}" ]; then
        echo "not installed: ${f}" >&2
        exit 1
    fi
done
reported=$(zpool-status-exporter --version 2>&1 | head -n 1)
case ${reported} in
    "zpool-status-exporter, version ${version} "*) ;;
    *)
        echo "zpool-status-exporter reports '${reported}', not version ${version}" >&2
        exit 1
        ;;
esac
dnf remove -y zpool-status-exporter
if command -v zpool-status-exporter > /dev/null 2>&1; then
    echo "zpool-status-exporter is still installed after dnf remove" >&2
    exit 1
fi
dnf install -y --setopt=tsflags= "${rpm}"
dnf clean all
echo "zpool-status-exporter ${version}: signature, install, version, remove, and reinstall passed"

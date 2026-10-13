#!/bin/sh
#
# Build a FreeBSD package from a prebuilt binary. Runs on FreeBSD only.
#
# Usage: build-pkg.sh <version> <path-to-freebsd-binary> <output-dir>
#
# The version is taken from a Git tag. A leading "v" is removed, and a
# release candidate suffix is rewritten to the form pkg(8) accepts:
#
#   v1.2.0         -> 1.2.0
#   v1.2.0-rc.1    -> 1.2.0.r1

set -eu

if [ "$#" -ne 3 ]; then
    echo "usage: $0 <version> <path-to-freebsd-binary> <output-dir>" >&2
    exit 64
fi

version=$1
binary=$2
outdir=$3

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "${here}/../.." && pwd)

version=${version#v}
version=$(printf '%s\n' "${version}" | sed -e 's/-rc\.\([0-9][0-9]*\)$/.r\1/')
case ${version} in
    *[!0-9A-Za-z._]* | '')
        echo "unsupported version: ${version}" >&2
        exit 65
        ;;
esac

if [ ! -f "${binary}" ]; then
    echo "binary not found: ${binary}" >&2
    exit 66
fi

stage=$(mktemp -d)
trap 'rm -rf "${stage}"' EXIT HUP INT TERM

install -d "${stage}/root/usr/local/bin" \
    "${stage}/root/usr/local/etc/rc.d" \
    "${stage}/root/usr/local/share/man/man8"
install -m 0555 "${binary}" "${stage}/root/usr/local/bin/zpool-status-exporter"
install -m 0555 "${here}/rc.d/zpool_status_exporter" \
    "${stage}/root/usr/local/etc/rc.d/zpool_status_exporter"
gzip -9nc "${root}/docs/zpool-status-exporter.8" \
    > "${stage}/root/usr/local/share/man/man8/zpool-status-exporter.8.gz"

sed -e "s/%%VERSION%%/${version}/" "${here}/+MANIFEST.in" > "${stage}/+MANIFEST"

cat > "${stage}/plist" <<PLIST
@(root,wheel,0555) bin/zpool-status-exporter
@(root,wheel,0555) etc/rc.d/zpool_status_exporter
@(root,wheel,0444) share/man/man8/zpool-status-exporter.8.gz
PLIST

mkdir -p "${outdir}"
pkg create -M "${stage}/+MANIFEST" -p "${stage}/plist" -r "${stage}/root" -o "${outdir}"

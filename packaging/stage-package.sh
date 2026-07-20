#!/bin/sh
set -eu

if [ "$#" -ne 4 ]; then
    echo "usage: $0 STAGE_ROOT VERSION ARCH LINUX_BINARY" >&2
    exit 2
fi

stage_root=$1
version=$2
architecture=$3
linux_binary=$4

case "$stage_root" in
    ""|/)
        echo "refusing unsafe package staging root" >&2
        exit 2
        ;;
esac

if [ ! -f "$linux_binary" ]; then
    echo "Linux binary not found: $linux_binary" >&2
    exit 2
fi

install -d -m 0755 \
    "$stage_root/DEBIAN" \
    "$stage_root/usr/bin" \
    "$stage_root/usr/lib/systemd/system" \
    "$stage_root/usr/share/doc/lts-agent/examples" \
    "$stage_root/usr/share/doc/lts-agent/examples/lbi"

install -m 0755 "$linux_binary" "$stage_root/usr/bin/lts-agent"
install -m 0644 packaging/systemd/lts-agent.service "$stage_root/usr/lib/systemd/system/lts-agent.service"
install -m 0644 packaging/systemd/lts-agent.timer "$stage_root/usr/lib/systemd/system/lts-agent.timer"
install -m 0644 README.md LICENSE CHANGELOG.md docs/LTS-CORE-API-v1.md docs/DEPLOYMENT.md docs/LBI-COMPATIBILITY.md docs/LBI-BUILD002-ACCEPTANCE.md docs/lbi-unprivileged-checks.patch "$stage_root/usr/share/doc/lts-agent/"
install -m 0644 configs/lts-agent.example.json configs/assigned.example.json "$stage_root/usr/share/doc/lts-agent/examples/"
install -m 0644 compat/lbi/lbi-unprivileged-checks.sh compat/lbi/lbi-validate.sh compat/lbi/health-check.sh "$stage_root/usr/share/doc/lts-agent/examples/lbi/"
install -m 0755 packaging/debian/postinst packaging/debian/prerm packaging/debian/postrm "$stage_root/DEBIAN/"

printf '%s\n' \
    'Package: lts-agent' \
    "Version: $version" \
    'Section: admin' \
    'Priority: optional' \
    "Architecture: $architecture" \
    'Maintainer: Likone Technologies' \
    'Depends: adduser, ca-certificates, systemd' \
    'Description: Likone Technology Stack node agent' \
    ' Collects local LBI inventory and optionally synchronizes the node with LTS Core.' \
    >"$stage_root/DEBIAN/control"

chmod 0644 "$stage_root/DEBIAN/control"

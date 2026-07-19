#!/usr/bin/env bash

set -euo pipefail

LBI_CHECK_LIBRARY="${LBI_CHECK_LIBRARY:-/opt/lts/scripts/lib/lbi-unprivileged-checks.sh}"
# shellcheck source=lbi-unprivileged-checks.sh
source "$LBI_CHECK_LIBRARY"

PASS=0
FAIL=0

check() {
    local description="$1"
    shift

    if "$@" >/dev/null 2>&1; then
        printf "[PASS] %s\n" "$description"
        PASS=$((PASS + 1))
    else
        printf "[FAIL] %s\n" "$description"
        FAIL=$((FAIL + 1))
    fi
}

echo
echo "========================================"
echo "      LBI Validation"
echo "========================================"
echo

check "Metadata exists" test -f /etc/lbi-release

check "Bootstrap exists" test -x /opt/lts/bootstrap/init.sh

check "LTS CLI exists" test -x /usr/local/bin/lts

check "LBI specification exists" test -f /opt/lts/spec/LBI-SPECIFICATION-v1.0.md

check "qemu-guest-agent running" systemctl is-active --quiet qemu-guest-agent

check "chrony running" systemctl is-active --quiet chrony

check "fail2ban running" systemctl is-active --quiet fail2ban

check "SSH service and LTS baseline valid" lbi_ssh_baseline_valid

check "Firewall service and baseline enabled" lbi_ufw_baseline_active

check "Hostname configured" hostnamectl

check "Directory /opt/apps" test -d /opt/apps

check "Directory /opt/data" test -d /opt/data

check "Directory /opt/backups" test -d /opt/backups

check "Directory /opt/logs" test -d /opt/logs

echo
echo "========================================"
echo "Passed : $PASS"
echo "Failed : $FAIL"
echo "========================================"

if [ "$FAIL" -eq 0 ]; then
    exit 0
else
    exit 1
fi

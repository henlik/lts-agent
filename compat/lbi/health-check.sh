#!/usr/bin/env bash

set -u

LBI_CHECK_LIBRARY="${LBI_CHECK_LIBRARY:-/opt/lts/scripts/lib/lbi-unprivileged-checks.sh}"
# shellcheck source=lbi-unprivileged-checks.sh
source "$LBI_CHECK_LIBRARY"

STATUS=0
WARNINGS=0
CRITICALS=0

pass() {
    printf '[OK] %s\n' "$1"
}

warn() {
    printf '[WARNING] %s\n' "$1"
    WARNINGS=$((WARNINGS + 1))
    [ "$STATUS" -lt 1 ] && STATUS=1
}

critical() {
    printf '[CRITICAL] %s\n' "$1"
    CRITICALS=$((CRITICALS + 1))
    STATUS=2
}

echo
echo "========================================"
echo "       LBI Runtime Health Check"
echo "========================================"
echo "Hostname : $(hostname)"
echo "Date     : $(date --iso-8601=seconds)"
echo

if [ -f /etc/lbi-release ]; then
    pass "LBI metadata is present"
else
    critical "Missing /etc/lbi-release"
fi

for service in qemu-guest-agent chrony fail2ban ssh; do
    if systemctl is-active --quiet "$service"; then
        pass "Service active: $service"
    else
        critical "Service inactive: $service"
    fi
done

if timedatectl show -p NTPSynchronized --value | grep -qx "yes"; then
    pass "System clock is synchronized"
else
    warn "System clock is not synchronized"
fi

FAILED_UNITS=$(systemctl --failed --no-legend 2>/dev/null | wc -l)

if [ "$FAILED_UNITS" -eq 0 ]; then
    pass "No failed systemd units"
else
    critical "$FAILED_UNITS systemd unit(s) failed"
fi

ROOT_USAGE=$(df -P / | awk 'NR==2 {gsub("%","",$5); print $5}')

if [ "$ROOT_USAGE" -ge 90 ]; then
    critical "Root filesystem usage is ${ROOT_USAGE}%"
elif [ "$ROOT_USAGE" -ge 80 ]; then
    warn "Root filesystem usage is ${ROOT_USAGE}%"
else
    pass "Root filesystem usage is ${ROOT_USAGE}%"
fi

MEM_AVAILABLE=$(awk '
/MemTotal:/ {total=$2}
/MemAvailable:/ {available=$2}
END {
    if (total > 0) {
        printf "%.0f", (available / total) * 100
    } else {
        print 0
    }
}' /proc/meminfo)

if [ "$MEM_AVAILABLE" -le 5 ]; then
    critical "Available memory is ${MEM_AVAILABLE}%"
elif [ "$MEM_AVAILABLE" -le 15 ]; then
    warn "Available memory is ${MEM_AVAILABLE}%"
else
    pass "Available memory is ${MEM_AVAILABLE}%"
fi

if ip route show default | grep -q "^default"; then
    pass "Default network route exists"
else
    critical "No default network route"
fi

if getent hosts security.ubuntu.com >/dev/null 2>&1; then
    pass "DNS resolution is working"
else
    warn "DNS resolution failed"
fi

if lbi_ufw_baseline_active; then
    pass "UFW firewall service and baseline are active"
else
    critical "UFW firewall service or baseline is inactive"
fi

echo
echo "========================================"
echo "Warnings  : $WARNINGS"
echo "Criticals : $CRITICALS"
echo "Exit code : $STATUS"
echo "========================================"

exit "$STATUS"

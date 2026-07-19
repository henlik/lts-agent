#!/usr/bin/env bash

# This library is sourced by the LBI validation and health scripts. The
# defaults are production paths; tests may override them without changing the
# host or requiring root.
LBI_CHECK_ETC_ROOT="${LBI_CHECK_ETC_ROOT:-/etc}"
LBI_CHECK_SYSTEMCTL="${LBI_CHECK_SYSTEMCTL:-systemctl}"
LBI_CHECK_STAT="${LBI_CHECK_STAT:-stat}"
LBI_CHECK_UFW_BIN="${LBI_CHECK_UFW_BIN:-/usr/sbin/ufw}"

lbi_secure_root_file() {
    local path="$1"
    local owner mode

    [ -f "$path" ] || return 1
    owner=$("$LBI_CHECK_STAT" -c '%U:%G' "$path") || return 1
    [ "$owner" = "root:root" ] || return 1
    mode=$("$LBI_CHECK_STAT" -c '%a' "$path") || return 1
    mode="${mode#0}"
    [ -n "$mode" ] || return 1
    (( (8#$mode & 8#022) == 0 ))
}

lbi_ssh_setting_is() {
    local path="$1"
    local expected_key="$2"
    local expected_value="$3"

    awk -v expected_key="$expected_key" -v expected_value="$expected_value" '
        BEGIN {
            expected_key = tolower(expected_key)
            expected_value = tolower(expected_value)
        }
        /^[[:space:]]*($|#)/ { next }
        tolower($1) == expected_key {
            seen++
            if (NF == 2 && tolower($2) == expected_value) {
                matched++
            }
        }
        END { exit !(seen == 1 && matched == 1) }
    ' "$path"
}

lbi_ssh_baseline_valid() {
    local path="$LBI_CHECK_ETC_ROOT/ssh/sshd_config.d/99-lts.conf"
    local requirement key value
    local requirements=(
        "PermitRootLogin no"
        "PubkeyAuthentication yes"
        "PasswordAuthentication yes"
        "KbdInteractiveAuthentication no"
        "PermitEmptyPasswords no"
        "X11Forwarding no"
        "AllowAgentForwarding yes"
        "AllowTcpForwarding yes"
        "MaxAuthTries 3"
        "ClientAliveInterval 300"
        "ClientAliveCountMax 2"
    )

    "$LBI_CHECK_SYSTEMCTL" is-active --quiet ssh || return 1
    lbi_secure_root_file "$path" || return 1
    if grep -Eiq '^[[:space:]]*Match([[:space:]]|$)' "$path"; then
        return 1
    fi

    for requirement in "${requirements[@]}"; do
        key="${requirement%% *}"
        value="${requirement#* }"
        lbi_ssh_setting_is "$path" "$key" "$value" || return 1
    done
}

lbi_ufw_baseline_active() {
    local ufw_config="$LBI_CHECK_ETC_ROOT/ufw/ufw.conf"
    local ufw_defaults="$LBI_CHECK_ETC_ROOT/default/ufw"

    [ -x "$LBI_CHECK_UFW_BIN" ] || return 1
    "$LBI_CHECK_SYSTEMCTL" is-enabled --quiet ufw || return 1
    "$LBI_CHECK_SYSTEMCTL" is-active --quiet ufw || return 1
    [ "$("$LBI_CHECK_SYSTEMCTL" show ufw -p Result --value)" = "success" ] || return 1
    [ "$("$LBI_CHECK_SYSTEMCTL" show ufw -p ExecMainStatus --value)" = "0" ] || return 1
    grep -Eq '^[[:space:]]*ENABLED[[:space:]]*=[[:space:]]*yes[[:space:]]*$' "$ufw_config" || return 1
    grep -Eq '^[[:space:]]*DEFAULT_INPUT_POLICY[[:space:]]*=[[:space:]]*"DROP"[[:space:]]*$' "$ufw_defaults" || return 1
    grep -Eq '^[[:space:]]*DEFAULT_OUTPUT_POLICY[[:space:]]*=[[:space:]]*"ACCEPT"[[:space:]]*$' "$ufw_defaults" || return 1
}

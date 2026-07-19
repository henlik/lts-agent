package lbi_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompatibilityScriptsAreValidAndUnprivileged(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"lbi-unprivileged-checks.sh", "lbi-validate.sh", "health-check.sh"} {
		path := filepath.Join(sourceDirectory(t), name)
		command := exec.Command("bash", "-n", path)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("bash -n %s: %v\n%s", name, err, output)
		}
	}

	combined := readSource(t, "lbi-unprivileged-checks.sh") + readSource(t, "lbi-validate.sh") + readSource(t, "health-check.sh")
	for _, forbidden := range []string{"sudo ", "sshd -t", "ufw status"} {
		if strings.Contains(combined, forbidden) {
			t.Errorf("compatibility scripts contain privileged command %q", strings.TrimSpace(forbidden))
		}
	}
}

func TestSSHBaselineValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*fixture)
		valid  bool
	}{
		{name: "valid", valid: true},
		{name: "service inactive", change: func(f *fixture) { f.environment = append(f.environment, "TEST_SSH_ACTIVE=fail") }},
		{name: "missing baseline", change: func(f *fixture) { mustRemove(t, f.sshConfig) }},
		{name: "wrong owner", change: func(f *fixture) { f.environment = append(f.environment, "TEST_OWNER=ltsadmin:ltsadmin") }},
		{name: "group writable", change: func(f *fixture) { f.environment = append(f.environment, "TEST_MODE=664") }},
		{name: "missing setting", change: func(f *fixture) { replaceFile(t, f.sshConfig, "PermitRootLogin no\n", "") }},
		{name: "wrong setting", change: func(f *fixture) { replaceFile(t, f.sshConfig, "X11Forwarding no", "X11Forwarding yes") }},
		{name: "duplicate setting", change: func(f *fixture) { appendFile(t, f.sshConfig, "\nMaxAuthTries 3\n") }},
		{name: "match block", change: func(f *fixture) { appendFile(t, f.sshConfig, "\nMatch User example\n") }},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newFixture(t)
			if test.change != nil {
				test.change(fixture)
			}
			if got := fixture.run("lbi_ssh_baseline_valid"); got != test.valid {
				t.Fatalf("lbi_ssh_baseline_valid() = %t, want %t", got, test.valid)
			}
		})
	}
}

func TestUFWBaselineValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*fixture)
		valid  bool
	}{
		{name: "valid", valid: true},
		{name: "unit disabled", change: func(f *fixture) { f.environment = append(f.environment, "TEST_UFW_ENABLED=fail") }},
		{name: "unit inactive", change: func(f *fixture) { f.environment = append(f.environment, "TEST_UFW_ACTIVE=fail") }},
		{name: "unit failed", change: func(f *fixture) { f.environment = append(f.environment, "TEST_UFW_RESULT=failed") }},
		{name: "unit nonzero", change: func(f *fixture) { f.environment = append(f.environment, "TEST_UFW_EXIT=1") }},
		{name: "executable missing", change: func(f *fixture) { mustRemove(t, f.ufwBinary) }},
		{name: "ufw disabled", change: func(f *fixture) { replaceFile(t, f.ufwConfig, "ENABLED=yes", "ENABLED=no") }},
		{name: "incoming accept", change: func(f *fixture) {
			replaceFile(t, f.ufwDefaults, `DEFAULT_INPUT_POLICY="DROP"`, `DEFAULT_INPUT_POLICY="ACCEPT"`)
		}},
		{name: "outgoing drop", change: func(f *fixture) {
			replaceFile(t, f.ufwDefaults, `DEFAULT_OUTPUT_POLICY="ACCEPT"`, `DEFAULT_OUTPUT_POLICY="DROP"`)
		}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newFixture(t)
			if test.change != nil {
				test.change(fixture)
			}
			if got := fixture.run("lbi_ufw_baseline_active"); got != test.valid {
				t.Fatalf("lbi_ufw_baseline_active() = %t, want %t", got, test.valid)
			}
		})
	}
}

type fixture struct {
	root        string
	helper      string
	sshConfig   string
	ufwConfig   string
	ufwDefaults string
	ufwBinary   string
	systemctl   string
	stat        string
	environment []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	fixture := &fixture{
		root:        root,
		helper:      filepath.Join(sourceDirectory(t), "lbi-unprivileged-checks.sh"),
		sshConfig:   filepath.Join(root, "etc", "ssh", "sshd_config.d", "99-lts.conf"),
		ufwConfig:   filepath.Join(root, "etc", "ufw", "ufw.conf"),
		ufwDefaults: filepath.Join(root, "etc", "default", "ufw"),
		ufwBinary:   filepath.Join(root, "usr", "sbin", "ufw"),
		systemctl:   filepath.Join(root, "bin", "systemctl"),
		stat:        filepath.Join(root, "bin", "stat"),
	}

	writeFile(t, fixture.sshConfig, []byte(sshBaseline), 0o644)
	writeFile(t, fixture.ufwConfig, []byte("ENABLED=yes\n"), 0o644)
	writeFile(t, fixture.ufwDefaults, []byte("DEFAULT_INPUT_POLICY=\"DROP\"\nDEFAULT_OUTPUT_POLICY=\"ACCEPT\"\n"), 0o644)
	writeFile(t, fixture.ufwBinary, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	writeFile(t, fixture.systemctl, []byte(systemctlStub), 0o755)
	writeFile(t, fixture.stat, []byte(statStub), 0o755)

	fixture.environment = append(os.Environ(),
		"LBI_CHECK_ETC_ROOT="+filepath.Join(root, "etc"),
		"LBI_CHECK_SYSTEMCTL="+fixture.systemctl,
		"LBI_CHECK_STAT="+fixture.stat,
		"LBI_CHECK_UFW_BIN="+fixture.ufwBinary,
		"TEST_OWNER=root:root",
		"TEST_MODE=644",
		"TEST_SSH_ACTIVE=ok",
		"TEST_UFW_ENABLED=ok",
		"TEST_UFW_ACTIVE=ok",
		"TEST_UFW_RESULT=success",
		"TEST_UFW_EXIT=0",
	)
	return fixture
}

func (f *fixture) run(function string) bool {
	command := exec.Command("bash", "-c", `source "$1"; "$2"`, "bash", f.helper, function)
	command.Env = f.environment
	return command.Run() == nil
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func replaceFile(t *testing.T, path, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), old, replacement, 1)
	if updated == string(data) {
		t.Fatalf("fixture %s does not contain %q", path, old)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, suffix string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(suffix); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func mustRemove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func readSource(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sourceDirectory(t), name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func sourceDirectory(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate compatibility tests")
	}
	return filepath.Dir(file)
}

const sshBaseline = `PermitRootLogin no
PubkeyAuthentication yes
PasswordAuthentication yes
KbdInteractiveAuthentication no
PermitEmptyPasswords no
X11Forwarding no
AllowAgentForwarding yes
AllowTcpForwarding yes
MaxAuthTries 3
ClientAliveInterval 300
ClientAliveCountMax 2
`

const systemctlStub = `#!/usr/bin/env bash
case "$1" in
    is-active)
        service="${3:-}"
        if [ "$service" = "ssh" ]; then
            [ "${TEST_SSH_ACTIVE:-fail}" = "ok" ]
        else
            [ "$service" = "ufw" ] && [ "${TEST_UFW_ACTIVE:-fail}" = "ok" ]
        fi
        ;;
    is-enabled)
        [ "${3:-}" = "ufw" ] && [ "${TEST_UFW_ENABLED:-fail}" = "ok" ]
        ;;
    show)
        case "${4:-}" in
            Result) printf '%s\n' "${TEST_UFW_RESULT:-failed}" ;;
            ExecMainStatus) printf '%s\n' "${TEST_UFW_EXIT:-1}" ;;
            *) exit 1 ;;
        esac
        ;;
    *) exit 1 ;;
esac
`

const statStub = `#!/usr/bin/env bash
case "${2:-}" in
    %U:%G) printf '%s\n' "${TEST_OWNER:-unknown:unknown}" ;;
    %a) printf '%s\n' "${TEST_MODE:-777}" ;;
    *) exit 1 ;;
esac
`

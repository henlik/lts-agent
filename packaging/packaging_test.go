package packaging_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/henlik/lts-agent/internal/agent"
)

func TestServiceUnitContract(t *testing.T) {
	t.Parallel()

	unit := readProjectFile(t, "packaging/systemd/lts-agent.service")
	required := []string{
		"Type=oneshot",
		"User=lts-agent",
		"Group=lts-agent",
		"WorkingDirectory=/var/lib/lts-agent",
		"ExecStart=/usr/bin/lts-agent",
		"TimeoutStartSec=15min",
		"UMask=0077",
		"StandardOutput=journal",
		"StandardError=journal",
		"NoNewPrivileges=true",
		"CapabilityBoundingSet=\n",
		"AmbientCapabilities=\n",
		"PrivateTmp=true",
		"PrivateDevices=true",
		"ProtectSystem=full",
		"ProtectHome=true",
		"ProtectKernelTunables=true",
		"ProtectKernelModules=true",
		"ProtectControlGroups=true",
		"RestrictSUIDSGID=true",
		"LockPersonality=true",
		"RestrictRealtime=true",
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6",
		"SystemCallArchitectures=native",
		"StateDirectory=lts-agent",
		"StateDirectoryMode=0700",
	}
	for _, directive := range required {
		if !strings.Contains(unit, directive) {
			t.Errorf("service unit missing %q", strings.TrimSpace(directive))
		}
	}
	for _, forbidden := range []string{"sudo", "Restart=", "User=root"} {
		if strings.Contains(unit, forbidden) {
			t.Errorf("service unit contains forbidden value %q", forbidden)
		}
	}
}

func TestTimerUnitContract(t *testing.T) {
	t.Parallel()

	unit := readProjectFile(t, "packaging/systemd/lts-agent.timer")
	for _, directive := range []string{
		"OnBootSec=2min",
		"OnUnitActiveSec=5min",
		"RandomizedDelaySec=30s",
		"AccuracySec=30s",
		"Persistent=true",
		"Unit=lts-agent.service",
		"WantedBy=timers.target",
	} {
		if !strings.Contains(unit, directive) {
			t.Errorf("timer unit missing %q", directive)
		}
	}
}

func TestMaintainerScriptsAreValidAndPreserveState(t *testing.T) {
	t.Parallel()

	root := projectRoot(t)
	for _, name := range []string{"postinst", "prerm", "postrm"} {
		path := filepath.Join(root, "packaging", "debian", name)
		command := exec.Command("sh", "-n", path)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("sh -n %s: %v\n%s", name, err, output)
		}
	}

	postinst := readProjectFile(t, "packaging/debian/postinst")
	for _, required := range []string{
		"addgroup --system lts-agent",
		"--shell /usr/sbin/nologin",
		"install -d -m 0700 -o lts-agent -g lts-agent /var/lib/lts-agent",
		"systemctl enable --now lts-agent.timer",
	} {
		if !strings.Contains(postinst, required) {
			t.Errorf("postinst missing %q", required)
		}
	}

	allRemovalScripts := readProjectFile(t, "packaging/debian/prerm") + readProjectFile(t, "packaging/debian/postrm")
	for _, forbidden := range []string{"rm -rf /var/lib/lts-agent", "userdel", "deluser", "delgroup"} {
		if strings.Contains(allRemovalScripts, forbidden) {
			t.Errorf("removal scripts contain destructive action %q", forbidden)
		}
	}
}

func TestPackageStagingLayoutAndModes(t *testing.T) {
	t.Parallel()

	root := projectRoot(t)
	temporary := t.TempDir()
	binary := filepath.Join(temporary, "lts-agent-linux-amd64")
	if err := os.WriteFile(binary, []byte("test-linux-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(temporary, "package-root")
	command := exec.Command("sh", "packaging/stage-package.sh", stage, agent.Version, "amd64", binary)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("stage package: %v\n%s", err, output)
	}

	expected := map[string]os.FileMode{
		"DEBIAN/control":    0o644,
		"DEBIAN/postinst":   0o755,
		"DEBIAN/prerm":      0o755,
		"DEBIAN/postrm":     0o755,
		"usr/bin/lts-agent": 0o755,
		"usr/lib/systemd/system/lts-agent.service":                0o644,
		"usr/lib/systemd/system/lts-agent.timer":                  0o644,
		"usr/share/doc/lts-agent/README.md":                       0o644,
		"usr/share/doc/lts-agent/LICENSE":                         0o644,
		"usr/share/doc/lts-agent/CHANGELOG.md":                    0o644,
		"usr/share/doc/lts-agent/LTS-CORE-API-v1.md":              0o644,
		"usr/share/doc/lts-agent/DEPLOYMENT.md":                   0o644,
		"usr/share/doc/lts-agent/examples/lts-agent.example.json": 0o644,
		"usr/share/doc/lts-agent/examples/assigned.example.json":  0o644,
	}
	for relative, mode := range expected {
		info, err := os.Stat(filepath.Join(stage, filepath.FromSlash(relative)))
		if err != nil {
			t.Errorf("staged file %s: %v", relative, err)
			continue
		}
		if got := info.Mode().Perm(); got != mode {
			t.Errorf("mode %s = %04o, want %04o", relative, got, mode)
		}
	}

	control, err := os.ReadFile(filepath.Join(stage, "DEBIAN", "control"))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"Package: lts-agent",
		"Version: " + agent.Version,
		"Architecture: amd64",
		"Depends: adduser, ca-certificates, systemd",
	} {
		if !strings.Contains(string(control), field) {
			t.Errorf("control file missing %q", field)
		}
	}
}

func TestMakefileVersionMatchesAgent(t *testing.T) {
	t.Parallel()

	makefile := readProjectFile(t, "Makefile")
	if !strings.Contains(makefile, "VERSION := "+agent.Version+"\n") {
		t.Fatalf("Makefile VERSION does not match agent version %s", agent.Version)
	}
}

func readProjectFile(t *testing.T, relative string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(projectRoot(t), filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(data)
}

func projectRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate packaging test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), ".."))
}

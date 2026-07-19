// Package system collects host and operating-system inventory. All direct OS
// interactions are represented by Dependencies to keep fallback behavior
// deterministic in unit tests.
package system

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/henlik/lts-agent/internal/inventory"
)

const (
	defaultOSReleasePath = "/etc/os-release"
	defaultKernelPath    = "/proc/sys/kernel/osrelease"
	defaultTimezonePath  = "/etc/timezone"
	defaultLocaltimePath = "/etc/localtime"
	zoneinfoMarker       = "/zoneinfo/"
)

// Dependencies contains only the operating-system operations used by Collector.
type Dependencies struct {
	ReadFile     func(string) ([]byte, error)
	Readlink     func(string) (string, error)
	Hostname     func() (string, error)
	RunCommand   func(context.Context, string, ...string) ([]byte, error)
	Architecture string
	Location     func() *time.Location
}

// Result holds the system portion of a report plus the separately modeled node
// hostname.
type Result struct {
	Hostname string
	System   inventory.System
}

// Collector gathers local inventory without requiring elevated privileges.
type Collector struct {
	deps Dependencies
}

// NewCollector constructs a collector using the current process environment.
func NewCollector() *Collector {
	return NewCollectorWithDependencies(Dependencies{
		ReadFile:     os.ReadFile,
		Readlink:     os.Readlink,
		Hostname:     os.Hostname,
		RunCommand:   runCommand,
		Architecture: runtime.GOARCH,
		Location:     func() *time.Location { return time.Now().Location() },
	})
}

// NewCollectorWithDependencies constructs a collector with injectable OS calls.
func NewCollectorWithDependencies(deps Dependencies) *Collector {
	return &Collector{deps: deps}
}

// Collect attempts every independent source even when an earlier source fails.
func (c *Collector) Collect(ctx context.Context) (Result, []inventory.Warning) {
	var result Result
	var warnings []inventory.Warning

	hostname, err := c.deps.Hostname()
	if err != nil {
		warnings = append(warnings, warning("hostname", err))
	} else {
		result.Hostname = strings.TrimSpace(hostname)
	}

	osName, err := collectOS(c.deps.ReadFile)
	if err != nil {
		warnings = append(warnings, warning("os", err))
	}
	result.System.OS = osName

	kernel, err := collectKernel(ctx, c.deps.ReadFile, c.deps.RunCommand)
	if err != nil {
		warnings = append(warnings, warning("kernel", err))
	}
	result.System.Kernel = kernel

	result.System.Architecture = NormalizeArchitecture(c.deps.Architecture)
	if result.System.Architecture == "" {
		warnings = append(warnings, warning("architecture", fmt.Errorf("architecture is unavailable")))
	}

	timezone, err := collectTimezone(c.deps.ReadFile, c.deps.Readlink, c.deps.Location)
	if err != nil {
		warnings = append(warnings, warning("timezone", err))
	}
	result.System.Timezone = timezone

	return result, warnings
}

func collectOS(readFile func(string) ([]byte, error)) (string, error) {
	data, err := readFile(defaultOSReleasePath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", defaultOSReleasePath, err)
	}

	values, parseWarnings := parseAssignments(data)
	if value := values["PRETTY_NAME"]; value != "" {
		return value, nil
	}

	name := values["NAME"]
	version := values["VERSION"]
	if value := strings.TrimSpace(strings.Join([]string{name, version}, " ")); value != "" {
		return value, nil
	}
	if len(parseWarnings) > 0 {
		return "", fmt.Errorf("parse %s: %s", defaultOSReleasePath, strings.Join(parseWarnings, "; "))
	}
	return "", fmt.Errorf("%s contains no usable operating-system name", defaultOSReleasePath)
}

func collectKernel(
	ctx context.Context,
	readFile func(string) ([]byte, error),
	run func(context.Context, string, ...string) ([]byte, error),
) (string, error) {
	data, fileErr := readFile(defaultKernelPath)
	if value := strings.TrimSpace(string(data)); fileErr == nil && value != "" {
		return value, nil
	}

	output, commandErr := run(ctx, "uname", "-r")
	if value := strings.TrimSpace(string(output)); commandErr == nil && value != "" {
		return value, nil
	}

	return "", fmt.Errorf("read %s: %v; run uname -r: %v", defaultKernelPath, fileErr, commandErr)
}

func collectTimezone(
	readFile func(string) ([]byte, error),
	readlink func(string) (string, error),
	location func() *time.Location,
) (string, error) {
	data, fileErr := readFile(defaultTimezonePath)
	if value := strings.TrimSpace(string(data)); fileErr == nil && value != "" {
		return value, nil
	}

	target, linkErr := readlink(defaultLocaltimePath)
	if linkErr == nil {
		// Relative symlinks are resolved before looking for the zoneinfo marker.
		if !filepath.IsAbs(target) {
			target = filepath.Clean(filepath.Join(filepath.Dir(defaultLocaltimePath), target))
		}
		if _, zone, found := strings.Cut(target, zoneinfoMarker); found && zone != "" {
			return zone, nil
		}
	}

	if current := location(); current != nil {
		name := current.String()
		if name != "" && name != "Local" {
			return name, nil
		}
	}

	return "", fmt.Errorf("read %s: %v; resolve %s: %v; runtime location unavailable", defaultTimezonePath, fileErr, defaultLocaltimePath, linkErr)
}

// NormalizeArchitecture converts Go architecture names to the values commonly
// reported by Linux tools and expected by the LBI inventory contract.
func NormalizeArchitecture(architecture string) string {
	switch strings.TrimSpace(architecture) {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "386":
		return "i386"
	default:
		return strings.TrimSpace(architecture)
	}
}

func parseAssignments(data []byte) (map[string]string, []string) {
	values := make(map[string]string)
	var warnings []string
	for index, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rawValue, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(key) == "" {
			warnings = append(warnings, fmt.Sprintf("line %d: expected KEY=VALUE", index+1))
			continue
		}
		value, err := parseAssignmentValue(strings.TrimSpace(rawValue))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("line %d (%s): %v", index+1, strings.TrimSpace(key), err))
			continue
		}
		values[strings.TrimSpace(key)] = value
	}
	return values, warnings
}

func parseAssignmentValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if value[0] == '\'' {
		if len(value) < 2 || value[len(value)-1] != '\'' {
			return "", fmt.Errorf("unterminated single-quoted value")
		}
		return value[1 : len(value)-1], nil
	}
	if value[0] == '"' {
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("invalid double-quoted value: %w", err)
		}
		return unquoted, nil
	}
	return value, nil
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	// CommandContext receives a fixed executable and argument list; no shell is
	// involved, so metadata can never become executable command text.
	return exec.CommandContext(ctx, name, args...).Output()
}

func warning(source string, err error) inventory.Warning {
	return inventory.Warning{Source: source, Message: err.Error()}
}

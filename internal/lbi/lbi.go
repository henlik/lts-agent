// Package lbi reads metadata from an LTS Base Image release file.
package lbi

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/henlik/lts-agent/internal/inventory"
)

// DefaultReleasePath is the metadata contract defined by the LBI specification.
const DefaultReleasePath = "/etc/lbi-release"

// ReadFileFunc is the narrow filesystem boundary needed by Collector. It is a
// named type so tests can supply fixtures without changing process-wide state.
type ReadFileFunc func(string) ([]byte, error)

// Collector reads LBI metadata without evaluating the file as shell code.
type Collector struct {
	path     string
	readFile ReadFileFunc
}

// NewCollector returns a collector for the production LBI release path.
func NewCollector() *Collector {
	return NewCollectorWithDependencies(DefaultReleasePath, os.ReadFile)
}

// NewCollectorWithDependencies permits deterministic tests and alternate roots.
func NewCollectorWithDependencies(path string, readFile ReadFileFunc) *Collector {
	return &Collector{path: path, readFile: readFile}
}

// Collect returns partial metadata and warnings rather than making a missing or
// damaged release file fatal to all other inventory collection.
func (c *Collector) Collect() (inventory.LBI, []inventory.Warning) {
	data, err := c.readFile(c.path)
	if err != nil {
		return inventory.LBI{Available: false}, []inventory.Warning{{
			Source:  "lbi",
			Message: fmt.Sprintf("read %s: %v", c.path, err),
		}}
	}

	values, parseWarnings := parseRelease(data)
	metadata := inventory.LBI{
		Available:  true,
		Name:       values["LBI_NAME"],
		Short:      values["LBI_SHORT"],
		Version:    values["LBI_VERSION"],
		Build:      values["LBI_BUILD"],
		BaseOS:     values["BASE_OS"],
		Maintainer: values["MAINTAINER"],
	}

	warnings := make([]inventory.Warning, 0, len(parseWarnings))
	for _, warning := range parseWarnings {
		warnings = append(warnings, inventory.Warning{Source: "lbi", Message: warning})
	}
	return metadata, warnings
}

// parseRelease accepts the simple KEY=VALUE subset used by release files. It
// deliberately does not expand variables, substitutions, or other shell syntax.
func parseRelease(data []byte) (map[string]string, []string) {
	values := make(map[string]string)
	var warnings []string

	for index, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, rawValue, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			warnings = append(warnings, fmt.Sprintf("line %d: expected KEY=VALUE", index+1))
			continue
		}

		value, err := parseValue(strings.TrimSpace(rawValue))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("line %d (%s): %v", index+1, key, err))
			continue
		}
		values[key] = value
	}

	return values, warnings
}

func parseValue(value string) (string, error) {
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

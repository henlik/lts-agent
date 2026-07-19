// Package role reads the roles and capabilities assigned to an LTS node.
// Version 0.2 inventories assignments but never applies or modifies them.
package role

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"

	"github.com/henlik/lts-agent/internal/inventory"
)

const (
	// DefaultAssignmentPath is the on-node assignment contract for v0.2.
	DefaultAssignmentPath = "/opt/lts/roles/assigned.json"
	// SupportedSchemaVersion identifies the only file schema understood by v0.2.
	SupportedSchemaVersion = 1
)

var identifierPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ReadFileFunc is the filesystem boundary needed by Collector.
type ReadFileFunc func(string) ([]byte, error)

// Collector reads and validates the local assignment document.
type Collector struct {
	path     string
	readFile ReadFileFunc
}

// NewCollector returns a collector for the production assignment path.
func NewCollector() *Collector {
	return NewCollectorWithDependencies(DefaultAssignmentPath, os.ReadFile)
}

// NewCollectorWithDependencies permits tests and alternate filesystem roots.
func NewCollectorWithDependencies(path string, readFile ReadFileFunc) *Collector {
	return &Collector{path: path, readFile: readFile}
}

type assignmentDocument struct {
	SchemaVersion *int      `json:"schema_version"`
	Roles         *[]string `json:"roles"`
	Capabilities  *[]string `json:"capabilities"`
}

// Collect returns an unavailable, empty assignment when the document cannot be
// read or its envelope is invalid. Invalid identifiers are isolated so valid
// assignments in the same otherwise-valid document remain useful.
func (c *Collector) Collect() (inventory.Assignment, []inventory.Warning) {
	empty := emptyAssignment()
	data, err := c.readFile(c.path)
	if err != nil {
		return empty, []inventory.Warning{{
			Source:  "assignment",
			Message: fmt.Sprintf("read %s: %v", c.path, err),
		}}
	}

	document, err := decodeDocument(data)
	if err != nil {
		return empty, []inventory.Warning{{
			Source:  "assignment",
			Message: fmt.Sprintf("parse %s: %v", c.path, err),
		}}
	}

	roles, roleWarnings := validateIdentifiers("assignment.roles", *document.Roles)
	capabilities, capabilityWarnings := validateIdentifiers("assignment.capabilities", *document.Capabilities)
	warnings := make([]inventory.Warning, 0, len(roleWarnings)+len(capabilityWarnings))
	warnings = append(warnings, roleWarnings...)
	warnings = append(warnings, capabilityWarnings...)

	return inventory.Assignment{
		Available:     true,
		SchemaVersion: SupportedSchemaVersion,
		Roles:         roles,
		Capabilities:  capabilities,
	}, warnings
}

func decodeDocument(data []byte) (assignmentDocument, error) {
	var document assignmentDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	// Strict fields catch administrator typos. Future formats must increment the
	// schema version and be explicitly supported rather than silently ignored.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return assignmentDocument{}, err
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return assignmentDocument{}, fmt.Errorf("unexpected trailing JSON value")
		}
		return assignmentDocument{}, fmt.Errorf("invalid trailing content: %w", err)
	}

	if document.SchemaVersion == nil {
		return assignmentDocument{}, fmt.Errorf("schema_version is required")
	}
	if *document.SchemaVersion != SupportedSchemaVersion {
		return assignmentDocument{}, fmt.Errorf("unsupported schema_version %d", *document.SchemaVersion)
	}
	if document.Roles == nil {
		return assignmentDocument{}, fmt.Errorf("roles must be an array")
	}
	if document.Capabilities == nil {
		return assignmentDocument{}, fmt.Errorf("capabilities must be an array")
	}

	return document, nil
}

func validateIdentifiers(source string, values []string) ([]string, []inventory.Warning) {
	unique := make(map[string]struct{}, len(values))
	var warnings []inventory.Warning
	for _, value := range values {
		if !identifierPattern.MatchString(value) {
			warnings = append(warnings, inventory.Warning{
				Source:  source,
				Message: fmt.Sprintf("invalid identifier %q; expected lowercase letters, digits, and internal hyphens", value),
			})
			continue
		}
		unique[value] = struct{}{}
	}

	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, warnings
}

func emptyAssignment() inventory.Assignment {
	return inventory.Assignment{
		Roles:        []string{},
		Capabilities: []string{},
	}
}

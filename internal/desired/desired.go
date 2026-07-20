// Package desired defines and strictly validates the LTS Core desired-state
// response. It reports intent only; it never applies roles or capabilities.
package desired

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"

	"github.com/henlik/lts-agent/internal/identifier"
	"github.com/henlik/lts-agent/internal/inventory"
)

const SupportedSchemaVersion = 1

var revisionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// Document is the strict wire representation returned by LTS Core.
type Document struct {
	SchemaVersion int
	Revision      string
	Roles         []string
	Capabilities  []string
}

type wireDocument struct {
	SchemaVersion *int             `json:"schema_version"`
	Revision      *string          `json:"revision"`
	Roles         *json.RawMessage `json:"roles"`
	Capabilities  *json.RawMessage `json:"capabilities"`
}

// UnmarshalJSON makes strict validation apply when Document is used directly
// as the response target of the shared Core client.
func (d *Document) UnmarshalJSON(data []byte) error {
	document, err := Parse(data)
	if err != nil {
		return err
	}
	*d = document
	return nil
}

// Parse validates one complete schema-v1 desired-state JSON value.
func Parse(data []byte) (Document, error) {
	var wire wireDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return Document{}, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Document{}, fmt.Errorf("unexpected trailing JSON value")
		}
		return Document{}, fmt.Errorf("invalid trailing content: %w", err)
	}

	if wire.SchemaVersion == nil {
		return Document{}, fmt.Errorf("schema_version is required")
	}
	if *wire.SchemaVersion != SupportedSchemaVersion {
		return Document{}, fmt.Errorf("unsupported schema_version %d", *wire.SchemaVersion)
	}
	if wire.Revision == nil {
		return Document{}, fmt.Errorf("revision is required")
	}
	if !revisionPattern.MatchString(*wire.Revision) {
		return Document{}, fmt.Errorf("revision is invalid")
	}

	roles, err := decodeIdentifiers("roles", wire.Roles)
	if err != nil {
		return Document{}, err
	}
	capabilities, err := decodeIdentifiers("capabilities", wire.Capabilities)
	if err != nil {
		return Document{}, err
	}
	return Document{
		SchemaVersion: *wire.SchemaVersion,
		Revision:      *wire.Revision,
		Roles:         roles,
		Capabilities:  capabilities,
	}, nil
}

// Inventory converts a validated document to the public output contract.
func (d Document) Inventory() inventory.DesiredState {
	return inventory.DesiredState{
		Available:     true,
		SchemaVersion: d.SchemaVersion,
		Revision:      d.Revision,
		Roles:         append([]string{}, d.Roles...),
		Capabilities:  append([]string{}, d.Capabilities...),
	}
}

func decodeIdentifiers(name string, raw *json.RawMessage) ([]string, error) {
	if raw == nil || bytes.Equal(bytes.TrimSpace(*raw), []byte("null")) {
		return nil, fmt.Errorf("%s must be a non-null array", name)
	}
	var values []string
	if err := json.Unmarshal(*raw, &values); err != nil {
		return nil, fmt.Errorf("%s must be an array of strings: %w", name, err)
	}
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !identifier.Valid(value) {
			return nil, fmt.Errorf("%s contains an invalid identifier", name)
		}
		unique[value] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

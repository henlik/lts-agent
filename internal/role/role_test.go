package role

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCollectorReadsSortsAndDeduplicatesAssignments(t *testing.T) {
	t.Parallel()

	data := []byte(`{
  "schema_version": 1,
  "roles": ["database-node", "application-node", "database-node"],
  "capabilities": ["postgresql", "docker", "docker"]
}`)
	collector := NewCollectorWithDependencies("/test/assigned.json", func(path string) ([]byte, error) {
		if path != "/test/assigned.json" {
			t.Fatalf("path = %q, want /test/assigned.json", path)
		}
		return data, nil
	})

	assignment, warnings := collector.Collect()
	if !assignment.Available || assignment.SchemaVersion != SupportedSchemaVersion {
		t.Fatalf("assignment = %#v, want available schema v1", assignment)
	}
	if want := []string{"application-node", "database-node"}; !reflect.DeepEqual(assignment.Roles, want) {
		t.Fatalf("Roles = %#v, want %#v", assignment.Roles, want)
	}
	if want := []string{"docker", "postgresql"}; !reflect.DeepEqual(assignment.Capabilities, want) {
		t.Fatalf("Capabilities = %#v, want %#v", assignment.Capabilities, want)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", warnings)
	}
}

func TestCollectorAcceptsEmptyArrays(t *testing.T) {
	t.Parallel()

	collector := collectorForJSON(`{"schema_version":1,"roles":[],"capabilities":[]}`)
	assignment, warnings := collector.Collect()
	if !assignment.Available || assignment.Roles == nil || assignment.Capabilities == nil {
		t.Fatalf("assignment = %#v, want available non-nil empty arrays", assignment)
	}
	if len(assignment.Roles) != 0 || len(assignment.Capabilities) != 0 || len(warnings) != 0 {
		t.Fatalf("assignment = %#v, warnings = %#v; want empty", assignment, warnings)
	}
}

func TestCollectorKeepsValidIdentifiersAndWarnsAboutInvalidOnes(t *testing.T) {
	t.Parallel()

	collector := collectorForJSON(`{
  "schema_version": 1,
  "roles": ["application-node", "Application Node", "-gateway", "gateway-1"],
  "capabilities": ["docker", "postgres_sql", "", "backup"]
}`)
	assignment, warnings := collector.Collect()

	if !assignment.Available {
		t.Fatal("Available = false, want true for a valid document")
	}
	if want := []string{"application-node", "gateway-1"}; !reflect.DeepEqual(assignment.Roles, want) {
		t.Fatalf("Roles = %#v, want %#v", assignment.Roles, want)
	}
	if want := []string{"backup", "docker"}; !reflect.DeepEqual(assignment.Capabilities, want) {
		t.Fatalf("Capabilities = %#v, want %#v", assignment.Capabilities, want)
	}
	if len(warnings) != 4 {
		t.Fatalf("warnings = %#v, want 4", warnings)
	}
	if warnings[0].Source != "assignment.roles" || warnings[2].Source != "assignment.capabilities" {
		t.Fatalf("warning sources = %#v, want roles followed by capabilities", warnings)
	}
}

func TestCollectorMarksReadFailureUnavailable(t *testing.T) {
	t.Parallel()

	collector := NewCollectorWithDependencies(DefaultAssignmentPath, func(string) ([]byte, error) {
		return nil, errors.New("permission denied")
	})
	assignment, warnings := collector.Collect()

	assertUnavailable(t, assignment.Available, assignment.SchemaVersion, assignment.Roles, assignment.Capabilities)
	if len(warnings) != 1 || warnings[0].Source != "assignment" || !strings.Contains(warnings[0].Message, "permission denied") {
		t.Fatalf("warnings = %#v, want one assignment read warning", warnings)
	}
}

func TestCollectorRejectsInvalidDocuments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "empty file", data: "", want: "EOF"},
		{name: "malformed JSON", data: `{`, want: "unexpected EOF"},
		{name: "trailing value", data: `{"schema_version":1,"roles":[],"capabilities":[]} {}`, want: "trailing JSON value"},
		{name: "invalid trailing content", data: `{"schema_version":1,"roles":[],"capabilities":[]} broken`, want: "invalid trailing content"},
		{name: "missing schema version", data: `{"roles":[],"capabilities":[]}`, want: "schema_version is required"},
		{name: "unsupported schema version", data: `{"schema_version":2,"roles":[],"capabilities":[]}`, want: "unsupported schema_version 2"},
		{name: "string schema version", data: `{"schema_version":"1","roles":[],"capabilities":[]}`, want: "cannot unmarshal string"},
		{name: "unknown field", data: `{"schema_version":1,"roles":[],"capabilities":[],"rolez":[]}`, want: "unknown field"},
		{name: "missing roles", data: `{"schema_version":1,"capabilities":[]}`, want: "roles must be an array"},
		{name: "null roles", data: `{"schema_version":1,"roles":null,"capabilities":[]}`, want: "roles must be an array"},
		{name: "missing capabilities", data: `{"schema_version":1,"roles":[]}`, want: "capabilities must be an array"},
		{name: "wrong array type", data: `{"schema_version":1,"roles":"app","capabilities":[]}`, want: "cannot unmarshal string"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assignment, warnings := collectorForJSON(test.data).Collect()
			assertUnavailable(t, assignment.Available, assignment.SchemaVersion, assignment.Roles, assignment.Capabilities)
			if len(warnings) != 1 || warnings[0].Source != "assignment" || !strings.Contains(warnings[0].Message, test.want) {
				t.Fatalf("warnings = %#v, want assignment warning containing %q", warnings, test.want)
			}
		})
	}
}

func collectorForJSON(data string) *Collector {
	return NewCollectorWithDependencies(DefaultAssignmentPath, func(string) ([]byte, error) {
		return []byte(data), nil
	})
}

func assertUnavailable(t *testing.T, available bool, schemaVersion int, roles, capabilities []string) {
	t.Helper()
	if available || schemaVersion != 0 {
		t.Fatalf("Available = %t, SchemaVersion = %d; want false, 0", available, schemaVersion)
	}
	if roles == nil || capabilities == nil || len(roles) != 0 || len(capabilities) != 0 {
		t.Fatalf("Roles = %#v, Capabilities = %#v; want non-nil empty arrays", roles, capabilities)
	}
}

package desired

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseValidNormalizesArrays(t *testing.T) {
	document, err := Parse([]byte(`{
  "schema_version": 1,
  "revision": "rev-123:one.two",
  "roles": ["worker-node", "application-node", "worker-node"],
  "capabilities": ["postgresql", "docker", "docker"]
}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if document.SchemaVersion != 1 || document.Revision != "rev-123:one.two" {
		t.Fatalf("Parse() document = %#v", document)
	}
	if want := []string{"application-node", "worker-node"}; !reflect.DeepEqual(document.Roles, want) {
		t.Fatalf("roles = %#v, want %#v", document.Roles, want)
	}
	if want := []string{"docker", "postgresql"}; !reflect.DeepEqual(document.Capabilities, want) {
		t.Fatalf("capabilities = %#v, want %#v", document.Capabilities, want)
	}
	output := document.Inventory()
	if !output.Available || output.Revision != document.Revision {
		t.Fatalf("Inventory() = %#v", output)
	}
}

func TestParseAcceptsEmptyArrays(t *testing.T) {
	document, err := Parse([]byte(`{"schema_version":1,"revision":"1","roles":[],"capabilities":[]}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if document.Roles == nil || document.Capabilities == nil {
		t.Fatalf("empty arrays must remain non-nil: %#v", document)
	}
}

func TestParseAcceptsMaximumRevisionLength(t *testing.T) {
	revision := "r" + strings.Repeat("._:-A0", 21) + "x"
	if len(revision) != 128 {
		t.Fatalf("test revision length = %d", len(revision))
	}
	_, err := Parse([]byte(`{"schema_version":1,"revision":"` + revision + `","roles":[],"capabilities":[]}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseRejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"malformed", `{`, "unexpected EOF"},
		{"trailing value", `{"schema_version":1,"revision":"r","roles":[],"capabilities":[]} {}`, "trailing"},
		{"trailing text", `{"schema_version":1,"revision":"r","roles":[],"capabilities":[]} x`, "trailing"},
		{"unknown", `{"schema_version":1,"revision":"r","roles":[],"capabilities":[],"extra":true}`, "unknown field"},
		{"missing schema", `{"revision":"r","roles":[],"capabilities":[]}`, "schema_version is required"},
		{"unsupported schema", `{"schema_version":2,"revision":"r","roles":[],"capabilities":[]}`, "unsupported schema_version"},
		{"missing revision", `{"schema_version":1,"roles":[],"capabilities":[]}`, "revision is required"},
		{"null revision", `{"schema_version":1,"revision":null,"roles":[],"capabilities":[]}`, "revision is required"},
		{"empty revision", `{"schema_version":1,"revision":"","roles":[],"capabilities":[]}`, "revision is invalid"},
		{"bad revision", `{"schema_version":1,"revision":"has space","roles":[],"capabilities":[]}`, "revision is invalid"},
		{"long revision", `{"schema_version":1,"revision":"` + strings.Repeat("r", 129) + `","roles":[],"capabilities":[]}`, "revision is invalid"},
		{"missing roles", `{"schema_version":1,"revision":"r","capabilities":[]}`, "roles must be"},
		{"null roles", `{"schema_version":1,"revision":"r","roles":null,"capabilities":[]}`, "roles must be"},
		{"wrong roles type", `{"schema_version":1,"revision":"r","roles":"node","capabilities":[]}`, "roles must be"},
		{"invalid role", `{"schema_version":1,"revision":"r","roles":["Bad-Role"],"capabilities":[]}`, "invalid identifier"},
		{"missing capabilities", `{"schema_version":1,"revision":"r","roles":[]}`, "capabilities must be"},
		{"null capabilities", `{"schema_version":1,"revision":"r","roles":[],"capabilities":null}`, "capabilities must be"},
		{"wrong capabilities type", `{"schema_version":1,"revision":"r","roles":[],"capabilities":[1]}`, "capabilities must be"},
		{"invalid capability", `{"schema_version":1,"revision":"r","roles":[],"capabilities":["-docker"]}`, "invalid identifier"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.input))
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Parse() error = %v, want containing %q", err, test.contains)
			}
		})
	}
}

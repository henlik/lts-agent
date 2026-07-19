package coresync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	coreclient "github.com/henlik/lts-agent/internal/core"
	"github.com/henlik/lts-agent/internal/inventory"
)

const testMachineID = "0123456789abcdef0123456789abcdef"

type clientCall struct {
	method   string
	endpoint string
	bearer   string
	request  any
}

type fakeClient struct {
	calls []clientCall
	do    func(string, string, any, any) error
}

func (c *fakeClient) DoJSONWithBearer(_ context.Context, method, endpoint, bearer string, request, response any) error {
	c.calls = append(c.calls, clientCall{method: method, endpoint: endpoint, bearer: bearer, request: request})
	if c.do != nil {
		return c.do(endpoint, bearer, request, response)
	}
	return nil
}

func TestSyncDisabledPerformsNoIO(t *testing.T) {
	t.Parallel()

	client := &fakeClient{}
	synchronizer := New(client, Options{Enabled: false})
	result, warnings := synchronizer.Sync(context.Background(), inventory.Report{})
	if result.Enabled || result.Registration.Status != "disabled" || result.Heartbeat.Status != "disabled" {
		t.Fatalf("result = %#v, want disabled", result)
	}
	if len(client.calls) != 0 || len(warnings) != 0 {
		t.Fatalf("calls = %#v, warnings = %#v", client.calls, warnings)
	}
}

func TestSyncRegistersPersistsDeletesTokenAndHeartbeats(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t)
	client := &fakeClient{do: func(endpoint, bearer string, request, response any) error {
		switch endpoint {
		case RegistrationEndpoint:
			if bearer != "enrollment-token" {
				t.Fatalf("registration bearer = %q", bearer)
			}
			document := request.(registrationRequest)
			if document.SchemaVersion != 1 || document.AgentVersion != "0.6.0" || document.Inventory.Core != nil {
				t.Fatalf("registration request = %#v", document)
			}
			hash := sha256.Sum256([]byte("lts-agent-registration-v1:" + testMachineID))
			if document.NodeFingerprint != hex.EncodeToString(hash[:]) {
				t.Fatalf("fingerprint = %q", document.NodeFingerprint)
			}
			*response.(*registrationResponse) = registrationResponse{SchemaVersion: 1, NodeID: "node-123", AgentToken: "node-token"}
		case "v1/nodes/node-123/heartbeat":
			if bearer != "node-token" {
				t.Fatalf("heartbeat bearer = %q", bearer)
			}
			document := request.(heartbeatRequest)
			if document.SchemaVersion != 1 || document.AgentVersion != "0.6.0" || document.Inventory.Core != nil {
				t.Fatalf("heartbeat request = %#v", document)
			}
			if document.SentAt != "2026-07-19T12:00:00.123456789Z" {
				t.Fatalf("SentAt = %q", document.SentAt)
			}
		default:
			t.Fatalf("unexpected endpoint %q", endpoint)
		}
		return nil
	}}
	report := inventory.Report{Agent: inventory.Agent{Version: "0.6.0"}, Core: &inventory.Core{Enabled: true}}
	synchronizer := NewWithDependencies(client, fixture.options(), fixture.dependencies())
	result, warnings := synchronizer.Sync(context.Background(), report)

	if !result.Enabled || !result.Registered || result.NodeID != "node-123" || result.Registration.Status != "succeeded" || !result.Registration.Attempted || result.Heartbeat.Status != "succeeded" || !result.Heartbeat.Attempted {
		t.Fatalf("result = %#v", result)
	}
	if len(warnings) != 0 || len(client.calls) != 2 {
		t.Fatalf("warnings = %#v, calls = %#v", warnings, client.calls)
	}
	for _, call := range client.calls {
		if call.method != http.MethodPost {
			t.Fatalf("method = %q, want POST", call.method)
		}
	}
	if _, err := os.Stat(fixture.tokenPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("enrollment token still exists: %v", err)
	}
	info, err := os.Stat(fixture.statePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %v, error = %v", info.Mode().Perm(), err)
	}
	var state stateDocument
	data, _ := os.ReadFile(fixture.statePath)
	if err := json.Unmarshal(data, &state); err != nil || state.NodeID != "node-123" || state.AgentToken != "node-token" {
		t.Fatalf("state = %#v, error = %v", state, err)
	}
}

func TestSyncUsesExistingStateWithoutEnrollment(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t)
	writeStateFixture(t, fixture.statePath, stateDocument{SchemaVersion: 1, NodeID: "existing-node", AgentToken: "existing-token"}, 0o600)
	if err := os.Remove(fixture.tokenPath); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{do: func(endpoint, bearer string, _ any, _ any) error {
		if endpoint != "v1/nodes/existing-node/heartbeat" || bearer != "existing-token" {
			t.Fatalf("endpoint = %q, bearer = %q", endpoint, bearer)
		}
		return nil
	}}
	result, warnings := NewWithDependencies(client, fixture.options(), fixture.dependencies()).Sync(context.Background(), inventory.Report{})
	if !result.Registered || result.Registration.Attempted || result.Registration.Status != "not_needed" || result.Heartbeat.Status != "succeeded" {
		t.Fatalf("result = %#v", result)
	}
	if len(client.calls) != 1 || len(warnings) != 0 {
		t.Fatalf("calls = %#v, warnings = %#v", client.calls, warnings)
	}
}

func TestSyncSanitizesRegistrationAndHeartbeatHTTPFailures(t *testing.T) {
	t.Parallel()

	t.Run("registration", func(t *testing.T) {
		fixture := newFixture(t)
		client := &fakeClient{do: func(string, string, any, any) error {
			return &coreclient.HTTPError{StatusCode: 401, Body: "enrollment-token"}
		}}
		result, warnings := NewWithDependencies(client, fixture.options(), fixture.dependencies()).Sync(context.Background(), inventory.Report{})
		if result.Registration.Status != "failed" || result.Heartbeat.Status != "skipped" || len(warnings) != 1 {
			t.Fatalf("result = %#v, warnings = %#v", result, warnings)
		}
		if strings.Contains(warnings[0].Message, "enrollment-token") || warnings[0].Message != "LTS Core returned HTTP 401" {
			t.Fatalf("warning leaked response body: %#v", warnings[0])
		}
	})

	t.Run("heartbeat", func(t *testing.T) {
		fixture := newFixture(t)
		writeStateFixture(t, fixture.statePath, stateDocument{SchemaVersion: 1, NodeID: "node-1", AgentToken: "node-token"}, 0o600)
		client := &fakeClient{do: func(string, string, any, any) error {
			return &coreclient.HTTPError{StatusCode: 403, Body: "node-token"}
		}}
		result, warnings := NewWithDependencies(client, fixture.options(), fixture.dependencies()).Sync(context.Background(), inventory.Report{})
		if !result.Registered || result.Heartbeat.Status != "failed" || len(warnings) != 1 || warnings[0].Message != "LTS Core returned HTTP 403" {
			t.Fatalf("result = %#v, warnings = %#v", result, warnings)
		}
		if _, err := os.Stat(fixture.statePath); err != nil {
			t.Fatalf("state was cleared: %v", err)
		}
	})
}

func TestSyncWarnsWhenConsumedTokenCannotBeRemoved(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t)
	client := successfulRegistrationClient()
	deps := fixture.dependencies()
	deps.Remove = func(path string) error {
		if path == fixture.tokenPath {
			return errors.New("directory is read-only")
		}
		return os.Remove(path)
	}
	result, warnings := NewWithDependencies(client, fixture.options(), deps).Sync(context.Background(), inventory.Report{})
	if result.Heartbeat.Status != "succeeded" || len(warnings) != 1 || warnings[0].Source != "core.enrollment" {
		t.Fatalf("result = %#v, warnings = %#v", result, warnings)
	}
}

func TestSyncRejectsInvalidStateWithoutOverwriting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		mode    os.FileMode
		setup   func(*testing.T, string)
		want    string
	}{
		{name: "loose permissions", content: `{"schema_version":1,"node_id":"node","agent_token":"token"}`, mode: 0o644, want: "permissions"},
		{name: "malformed", content: `{`, mode: 0o600, want: "parse Core state"},
		{name: "unknown field", content: `{"schema_version":1,"node_id":"node","agent_token":"token","extra":true}`, mode: 0o600, want: "unknown field"},
		{name: "unsupported schema", content: `{"schema_version":2,"node_id":"node","agent_token":"token"}`, mode: 0o600, want: "unsupported schema_version"},
		{name: "invalid node", content: `{"schema_version":1,"node_id":"bad/node","agent_token":"token"}`, mode: 0o600, want: "invalid node_id"},
		{name: "invalid token", content: `{"schema_version":1,"node_id":"node","agent_token":"bad token"}`, mode: 0o600, want: "invalid agent_token"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			if err := os.WriteFile(fixture.statePath, []byte(test.content), test.mode); err != nil {
				t.Fatal(err)
			}
			client := &fakeClient{}
			result, warnings := NewWithDependencies(client, fixture.options(), fixture.dependencies()).Sync(context.Background(), inventory.Report{})
			if result.Registration.Status != "failed" || len(client.calls) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0].Message, test.want) {
				t.Fatalf("result = %#v, calls = %#v, warnings = %#v", result, client.calls, warnings)
			}
		})
	}

	t.Run("symlink", func(t *testing.T) {
		fixture := newFixture(t)
		target := filepath.Join(fixture.directory, "target")
		writeStateFixture(t, target, stateDocument{SchemaVersion: 1, NodeID: "node", AgentToken: "token"}, 0o600)
		if err := os.Symlink(target, fixture.statePath); err != nil {
			t.Fatal(err)
		}
		result, warnings := NewWithDependencies(&fakeClient{}, fixture.options(), fixture.dependencies()).Sync(context.Background(), inventory.Report{})
		if result.Registration.Status != "failed" || len(warnings) != 1 || !strings.Contains(warnings[0].Message, "non-symlink") {
			t.Fatalf("result = %#v, warnings = %#v", result, warnings)
		}
	})
}

func TestSyncReportsRegistrationPrerequisiteAndPersistenceFailures(t *testing.T) {
	t.Parallel()

	t.Run("missing machine ID", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.machineIDPath = filepath.Join(fixture.directory, "missing-machine-id")
		result, warnings := NewWithDependencies(&fakeClient{}, fixture.options(), fixture.dependencies()).Sync(context.Background(), inventory.Report{})
		if result.Registration.Status != "failed" || len(warnings) != 1 || !strings.Contains(warnings[0].Message, "read machine ID") {
			t.Fatalf("result = %#v, warnings = %#v", result, warnings)
		}
	})

	t.Run("invalid machine ID", func(t *testing.T) {
		fixture := newFixture(t)
		if err := os.WriteFile(fixture.machineIDPath, []byte("not-a-machine-id"), 0o644); err != nil {
			t.Fatal(err)
		}
		result, warnings := NewWithDependencies(&fakeClient{}, fixture.options(), fixture.dependencies()).Sync(context.Background(), inventory.Report{})
		if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "32 hexadecimal") {
			t.Fatalf("result = %#v, warnings = %#v", result, warnings)
		}
	})

	t.Run("missing enrollment token", func(t *testing.T) {
		fixture := newFixture(t)
		if err := os.Remove(fixture.tokenPath); err != nil {
			t.Fatal(err)
		}
		result, warnings := NewWithDependencies(&fakeClient{}, fixture.options(), fixture.dependencies()).Sync(context.Background(), inventory.Report{})
		if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "inspect enrollment token") {
			t.Fatalf("result = %#v, warnings = %#v", result, warnings)
		}
	})

	t.Run("loose enrollment permissions", func(t *testing.T) {
		fixture := newFixture(t)
		if err := os.Chmod(fixture.tokenPath, 0o644); err != nil {
			t.Fatal(err)
		}
		result, warnings := NewWithDependencies(&fakeClient{}, fixture.options(), fixture.dependencies()).Sync(context.Background(), inventory.Report{})
		if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "permissions") {
			t.Fatalf("result = %#v, warnings = %#v", result, warnings)
		}
	})

	t.Run("enrollment symlink", func(t *testing.T) {
		fixture := newFixture(t)
		target := filepath.Join(fixture.directory, "real-token")
		if err := os.Rename(fixture.tokenPath, target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, fixture.tokenPath); err != nil {
			t.Fatal(err)
		}
		result, warnings := NewWithDependencies(&fakeClient{}, fixture.options(), fixture.dependencies()).Sync(context.Background(), inventory.Report{})
		if result.Registration.Status != "failed" || len(warnings) != 1 || !strings.Contains(warnings[0].Message, "non-symlink") {
			t.Fatalf("result = %#v, warnings = %#v", result, warnings)
		}
	})

	t.Run("loose state directory permissions", func(t *testing.T) {
		fixture := newFixture(t)
		stateDirectory := filepath.Join(fixture.directory, "state")
		if err := os.Mkdir(stateDirectory, 0o755); err != nil {
			t.Fatal(err)
		}
		fixture.statePath = filepath.Join(stateDirectory, "state.json")
		result, warnings := NewWithDependencies(successfulRegistrationClient(), fixture.options(), fixture.dependencies()).Sync(context.Background(), inventory.Report{})
		if result.Registered || result.Registration.Status != "failed" || len(warnings) != 1 || !strings.Contains(warnings[0].Message, "state directory permissions") {
			t.Fatalf("result = %#v, warnings = %#v", result, warnings)
		}
	})

	t.Run("state rename failure", func(t *testing.T) {
		fixture := newFixture(t)
		deps := fixture.dependencies()
		deps.Rename = func(string, string) error { return errors.New("rename denied") }
		result, warnings := NewWithDependencies(successfulRegistrationClient(), fixture.options(), deps).Sync(context.Background(), inventory.Report{})
		if result.Registered || result.Registration.Status != "failed" || len(warnings) != 1 || !strings.Contains(warnings[0].Message, "persist Core state") {
			t.Fatalf("result = %#v, warnings = %#v", result, warnings)
		}
		if _, err := os.Stat(fixture.tokenPath); err != nil {
			t.Fatalf("token removed after persistence failure: %v", err)
		}
	})
}

func TestRegistrationResponseStrictValidation(t *testing.T) {
	t.Parallel()

	tests := []string{
		`{"schema_version":1,"node_id":"node","agent_token":"token","extra":true}`,
		`{"schema_version":1,"node_id":"node","agent_token":"token"} {}`,
		`{`,
	}
	for _, data := range tests {
		var response registrationResponse
		if err := json.Unmarshal([]byte(data), &response); err == nil {
			t.Errorf("Unmarshal(%q) error = nil", data)
		}
	}
}

func TestFingerprintIsDeterministicAndCaseInsensitive(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t)
	synchronizer := NewWithDependencies(&fakeClient{}, fixture.options(), fixture.dependencies())
	first, err := synchronizer.fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.machineIDPath, []byte(strings.ToUpper(testMachineID)), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := synchronizer.fingerprint()
	if err != nil || first != second || len(first) != 64 {
		t.Fatalf("fingerprints = %q, %q; error = %v", first, second, err)
	}
}

type fixture struct {
	directory     string
	machineIDPath string
	tokenPath     string
	statePath     string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := fixture{
		directory:     directory,
		machineIDPath: filepath.Join(directory, "machine-id"),
		tokenPath:     filepath.Join(directory, "enrollment-token"),
		statePath:     filepath.Join(directory, "state.json"),
	}
	if err := os.WriteFile(fixture.machineIDPath, []byte(testMachineID+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.tokenPath, []byte("enrollment-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (f fixture) options() Options {
	return Options{
		Enabled:             true,
		AgentVersion:        "0.6.0",
		MachineIDPath:       f.machineIDPath,
		EnrollmentTokenFile: f.tokenPath,
		StateFile:           f.statePath,
	}
}

func (f fixture) dependencies() Dependencies {
	return Dependencies{
		ReadFile:   os.ReadFile,
		Lstat:      os.Lstat,
		MkdirAll:   os.MkdirAll,
		CreateTemp: os.CreateTemp,
		Rename:     os.Rename,
		Remove:     os.Remove,
		Now: func() time.Time {
			return time.Date(2026, 7, 19, 12, 0, 0, 123456789, time.UTC)
		},
	}
}

func successfulRegistrationClient() *fakeClient {
	return &fakeClient{do: func(endpoint, _ string, _ any, response any) error {
		if endpoint == RegistrationEndpoint {
			*response.(*registrationResponse) = registrationResponse{SchemaVersion: 1, NodeID: "node-123", AgentToken: "node-token"}
		}
		return nil
	}}
}

func writeStateFixture(t *testing.T, path string, state stateDocument, mode os.FileMode) {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func TestHeartbeatRequestShape(t *testing.T) {
	t.Parallel()

	want := []string{"schema_version", "sent_at", "agent_version", "inventory"}
	data, err := json.Marshal(heartbeatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(fields))
	for _, key := range want {
		if _, ok := fields[key]; ok {
			got = append(got, key)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("heartbeat fields = %#v, want %#v", got, want)
	}
	if http.MethodPost != "POST" {
		t.Fatal("unreachable method contract")
	}
}

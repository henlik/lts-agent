package config

import (
	"errors"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	t.Parallel()

	got := Defaults()
	if got.SchemaVersion != 2 || got.Checks.TimeoutSeconds != 30 || got.Checks.MaxOutputBytes != 65536 || got.Logging.Level != "info" {
		t.Fatalf("Defaults() = %#v", got)
	}
	if got.Checks.ValidationPath != "/opt/lts/scripts/lbi-validate.sh" || got.Checks.HealthPath != "/opt/lts/scripts/health-check.sh" {
		t.Fatalf("default paths = %q, %q", got.Checks.ValidationPath, got.Checks.HealthPath)
	}
	if got.Core.Enabled || got.Core.RequestTimeoutSeconds != 10 || got.Core.EnrollmentTokenFile != "/var/lib/lts-agent/enrollment-token" || got.Core.StateFile != "/var/lib/lts-agent/state.json" {
		t.Fatalf("default Core config = %#v", got.Core)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	t.Parallel()

	configuration, loaded, err := LoadWithDependencies(DefaultPath, func(string) ([]byte, error) {
		return nil, &os.PathError{Op: "open", Path: DefaultPath, Err: os.ErrNotExist}
	})
	if err != nil || loaded || !reflect.DeepEqual(configuration, Defaults()) {
		t.Fatalf("LoadWithDependencies() = %#v, %t, %v; want defaults, false, nil", configuration, loaded, err)
	}
}

func TestLoadReadFailureIsFatal(t *testing.T) {
	t.Parallel()

	_, loaded, err := LoadWithDependencies(DefaultPath, func(string) ([]byte, error) {
		return nil, errors.New("permission denied")
	})
	if loaded || err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("LoadWithDependencies() loaded = %t, error = %v", loaded, err)
	}
}

func TestParseFullConfiguration(t *testing.T) {
	t.Parallel()

	data := []byte(`{
  "schema_version": 1,
  "checks": {
    "validation_path": "/custom/validate",
    "health_path": "/custom/health",
    "timeout_seconds": 45,
    "max_output_bytes": 131072
  },
  "logging": {"level": "debug"}
}`)
	configuration, err := parse(data)
	if err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	want := Config{
		SchemaVersion: 1,
		Checks: Checks{
			ValidationPath: "/custom/validate",
			HealthPath:     "/custom/health",
			TimeoutSeconds: 45,
			MaxOutputBytes: 131072,
		},
		Logging: Logging{Level: "debug"},
		Core:    Defaults().Core,
	}
	if !reflect.DeepEqual(configuration, want) {
		t.Fatalf("parse() = %#v, want %#v", configuration, want)
	}
}

func TestParsePartialConfigurationMergesDefaults(t *testing.T) {
	t.Parallel()

	configuration, err := parse([]byte(`{"schema_version":1,"checks":{"timeout_seconds":60}}`))
	if err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	want := Defaults()
	want.SchemaVersion = 1
	want.Checks.TimeoutSeconds = 60
	if !reflect.DeepEqual(configuration, want) {
		t.Fatalf("parse() = %#v, want %#v", configuration, want)
	}
}

func TestParseRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "empty", data: "", want: "EOF"},
		{name: "malformed", data: `{`, want: "unexpected EOF"},
		{name: "trailing value", data: `{"schema_version":1} {}`, want: "trailing JSON value"},
		{name: "invalid trailing content", data: `{"schema_version":1} broken`, want: "invalid trailing content"},
		{name: "unknown top-level field", data: `{"schema_version":1,"unknown":true}`, want: "unknown field"},
		{name: "missing schema", data: `{}`, want: "schema_version is required"},
		{name: "unsupported schema", data: `{"schema_version":3}`, want: "unsupported schema_version 3"},
		{name: "wrong schema type", data: `{"schema_version":"1"}`, want: "cannot unmarshal string"},
		{name: "null checks", data: `{"schema_version":1,"checks":null}`, want: "checks must be an object"},
		{name: "wrong checks type", data: `{"schema_version":1,"checks":[]}`, want: "cannot unmarshal array"},
		{name: "unknown checks field", data: `{"schema_version":1,"checks":{"timeuot_seconds":30}}`, want: "unknown field"},
		{name: "null check field", data: `{"schema_version":1,"checks":{"timeout_seconds":null}}`, want: "checks.timeout_seconds must not be null"},
		{name: "null logging", data: `{"schema_version":1,"logging":null}`, want: "logging must be an object"},
		{name: "wrong logging type", data: `{"schema_version":1,"logging":[]}`, want: "cannot unmarshal array"},
		{name: "unknown logging field", data: `{"schema_version":1,"logging":{"format":"json"}}`, want: "unknown field"},
		{name: "null log level", data: `{"schema_version":1,"logging":{"level":null}}`, want: "logging.level must not be null"},
		{name: "relative validation path", data: `{"schema_version":1,"checks":{"validation_path":"validate.sh"}}`, want: "validation_path"},
		{name: "empty health path", data: `{"schema_version":1,"checks":{"health_path":""}}`, want: "health_path"},
		{name: "timeout too low", data: `{"schema_version":1,"checks":{"timeout_seconds":0}}`, want: "between 1 and 300"},
		{name: "timeout too high", data: `{"schema_version":1,"checks":{"timeout_seconds":301}}`, want: "between 1 and 300"},
		{name: "output too low", data: `{"schema_version":1,"checks":{"max_output_bytes":1023}}`, want: "between 1024 and 1048576"},
		{name: "output too high", data: `{"schema_version":1,"checks":{"max_output_bytes":1048577}}`, want: "between 1024 and 1048576"},
		{name: "invalid log level", data: `{"schema_version":1,"logging":{"level":"INFO"}}`, want: "logging.level"},
		{name: "wrong timeout type", data: `{"schema_version":1,"checks":{"timeout_seconds":"30"}}`, want: "cannot unmarshal string"},
		{name: "core in v1", data: `{"schema_version":1,"core":{"enabled":false}}`, want: "core requires schema_version 2"},
		{name: "null core", data: `{"schema_version":2,"core":null}`, want: "core must be an object"},
		{name: "unknown core field", data: `{"schema_version":2,"core":{"endpoint":"https://core.example"}}`, want: "unknown field"},
		{name: "null core field", data: `{"schema_version":2,"core":{"enabled":null}}`, want: "core.enabled must not be null"},
		{name: "enabled without URL", data: `{"schema_version":2,"core":{"enabled":true}}`, want: "base_url is required"},
		{name: "HTTP Core URL", data: `{"schema_version":2,"core":{"base_url":"http://core.example"}}`, want: "HTTPS origin"},
		{name: "Core URL credentials", data: `{"schema_version":2,"core":{"base_url":"https://user@core.example"}}`, want: "HTTPS origin"},
		{name: "relative CA", data: `{"schema_version":2,"core":{"ca_file":"ca.pem"}}`, want: "ca_file"},
		{name: "relative enrollment", data: `{"schema_version":2,"core":{"enrollment_token_file":"token"}}`, want: "enrollment_token_file"},
		{name: "relative state", data: `{"schema_version":2,"core":{"state_file":"state"}}`, want: "state_file"},
		{name: "Core timeout low", data: `{"schema_version":2,"core":{"request_timeout_seconds":0}}`, want: "between 1 and 120"},
		{name: "Core timeout high", data: `{"schema_version":2,"core":{"request_timeout_seconds":121}}`, want: "between 1 and 120"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := parse([]byte(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parse() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestParseSchemaV2CoreOverrides(t *testing.T) {
	t.Parallel()

	configuration, err := parse([]byte(`{
  "schema_version": 2,
  "core": {
    "enabled": true,
    "base_url": "https://core.example/api/",
    "ca_file": "/opt/lts/config/core-ca.pem",
    "request_timeout_seconds": 15,
    "enrollment_token_file": "/secure/enrollment-token",
    "state_file": "/state/node.json"
  }
}`))
	if err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	want := Core{
		Enabled:               true,
		BaseURL:               "https://core.example/api/",
		CAFile:                "/opt/lts/config/core-ca.pem",
		RequestTimeoutSeconds: 15,
		EnrollmentTokenFile:   "/secure/enrollment-token",
		StateFile:             "/state/node.json",
	}
	if !reflect.DeepEqual(configuration.Core, want) {
		t.Fatalf("Core = %#v, want %#v", configuration.Core, want)
	}
}

func TestLogLevel(t *testing.T) {
	t.Parallel()

	tests := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	for value, want := range tests {
		got, err := LogLevel(value)
		if err != nil || got != want {
			t.Errorf("LogLevel(%q) = %v, %v; want %v, nil", value, got, err, want)
		}
	}
	if _, err := LogLevel("trace"); err == nil {
		t.Fatal("LogLevel(trace) error = nil")
	}
}

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/henlik/lts-agent/internal/agent"
	"github.com/henlik/lts-agent/internal/config"
	"github.com/henlik/lts-agent/internal/inventory"
)

type fakeRunner struct {
	report inventory.Report
	calls  int
}

type fakeCoreSynchronizer struct {
	result   *inventory.Core
	warnings []inventory.Warning
	calls    int
}

func (s *fakeCoreSynchronizer) Sync(context.Context, inventory.Report) (*inventory.Core, []inventory.Warning) {
	s.calls++
	return s.result, s.warnings
}

func (r *fakeRunner) Collect(context.Context) inventory.Report {
	r.calls++
	return r.report
}

func TestRunLogsLifecycleAndKeepsStdoutAsInventory(t *testing.T) {
	t.Parallel()

	report := inventory.Report{
		Agent: inventory.Agent{Version: agent.Version},
		Checks: inventory.Checks{
			Validation: inventory.Check{Status: "passed"},
			Health:     inventory.Check{Status: "degraded"},
		},
		Warnings: []inventory.Warning{
			{Source: "lbi", Message: "missing metadata"},
			{Source: "checks.health", Message: "slow response"},
		},
	}
	runner := &fakeRunner{report: report}
	configuration := config.Defaults()
	configuration.Checks.TimeoutSeconds = 45

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var factoryConfig config.Config
	application := App{
		Stdout: &stdout,
		Stderr: &stderr,
		LoadConfig: func() (config.Config, bool, error) {
			return configuration, true, nil
		},
		NewRunner: func(got config.Config) Runner {
			factoryConfig = got
			return runner
		},
		Now:        time.Now,
		ConfigPath: config.DefaultPath,
	}

	if exitCode := application.Run(context.Background()); exitCode != 0 {
		t.Fatalf("Run() = %d, want 0", exitCode)
	}
	if runner.calls != 1 {
		t.Fatalf("Collect() calls = %d, want 1", runner.calls)
	}
	if !reflect.DeepEqual(factoryConfig, configuration) {
		t.Fatalf("factory config = %#v, want %#v", factoryConfig, configuration)
	}

	var decoded inventory.Report
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout is not one inventory JSON document: %v\n%s", err, stdout.String())
	}
	if decoded.Agent.Version != agent.Version || decoded.Checks.Health.Status != "degraded" {
		t.Fatalf("stdout report = %#v", decoded)
	}

	logs := decodeLogs(t, stderr.String())
	wantEvents := []string{"config_loaded", "collection_started", "inventory_warning", "inventory_warning", "collection_completed"}
	if got := logEvents(logs); !reflect.DeepEqual(got, wantEvents) {
		t.Fatalf("events = %#v, want %#v", got, wantEvents)
	}
	for _, entry := range logs {
		if entry["agent_version"] != agent.Version {
			t.Fatalf("log lacks agent version: %#v", entry)
		}
	}
	if logs[2]["source"] != "lbi" || logs[2]["detail"] != "missing metadata" {
		t.Fatalf("first warning log = %#v", logs[2])
	}
	if logs[4]["warning_count"] != float64(2) || logs[4]["health_status"] != "degraded" {
		t.Fatalf("completion log = %#v", logs[4])
	}
}

func TestRunLogsDefaultConfigurationSource(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	application := testApp(&stdout, &stderr, config.Defaults(), false, &fakeRunner{})
	if exitCode := application.Run(context.Background()); exitCode != 0 {
		t.Fatalf("Run() = %d, want 0", exitCode)
	}
	logs := decodeLogs(t, stderr.String())
	if logs[0]["event"] != "config_defaults_used" {
		t.Fatalf("first log = %#v, want config_defaults_used", logs[0])
	}
}

func TestRunRespectsWarningLogLevel(t *testing.T) {
	t.Parallel()

	configuration := config.Defaults()
	configuration.Logging.Level = "warn"
	runner := &fakeRunner{report: inventory.Report{
		Warnings: []inventory.Warning{{Source: "system", Message: "partial"}},
	}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	application := testApp(&stdout, &stderr, configuration, true, runner)
	if exitCode := application.Run(context.Background()); exitCode != 0 {
		t.Fatalf("Run() = %d, want 0", exitCode)
	}
	logs := decodeLogs(t, stderr.String())
	if len(logs) != 1 || logs[0]["event"] != "inventory_warning" || logs[0]["level"] != "WARN" {
		t.Fatalf("logs = %#v, want one warning", logs)
	}
}

func TestRunInvalidConfigurationIsFatalWithoutInventory(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	application := App{
		Stdout: &stdout,
		Stderr: &stderr,
		LoadConfig: func() (config.Config, bool, error) {
			return config.Config{}, true, errors.New("unsupported schema")
		},
		NewRunner:  func(config.Config) Runner { t.Fatal("runner must not be created"); return nil },
		Now:        time.Now,
		ConfigPath: config.DefaultPath,
	}
	if exitCode := application.Run(context.Background()); exitCode != 1 {
		t.Fatalf("Run() = %d, want 1", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	logs := decodeLogs(t, stderr.String())
	if len(logs) != 1 || logs[0]["event"] != "config_invalid" || logs[0]["level"] != "ERROR" {
		t.Fatalf("logs = %#v, want one config_invalid error", logs)
	}
}

func TestRunOutputFailureIsLoggedAndFatal(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	application := testApp(failingWriter{}, &stderr, config.Defaults(), true, &fakeRunner{})
	if exitCode := application.Run(context.Background()); exitCode != 1 {
		t.Fatalf("Run() = %d, want 1", exitCode)
	}
	logs := decodeLogs(t, stderr.String())
	events := logEvents(logs)
	if events[len(events)-1] != "inventory_write_failed" || contains(events, "collection_completed") {
		t.Fatalf("events = %#v, want write failure without completion", events)
	}
}

func TestRunSynchronizesCoreAndLogsNonfatalFailure(t *testing.T) {
	t.Parallel()

	configuration := config.Defaults()
	configuration.Core.Enabled = true
	configuration.Core.BaseURL = "https://core.example"
	synchronizer := &fakeCoreSynchronizer{
		result: &inventory.Core{
			Enabled:      true,
			Registered:   true,
			NodeID:       "node-123",
			Registration: inventory.Operation{Status: "not_needed"},
			Heartbeat:    inventory.Operation{Attempted: true, Status: "failed"},
		},
		warnings: []inventory.Warning{{Source: "core.heartbeat", Message: "LTS Core returned HTTP 503"}},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	application := testApp(&stdout, &stderr, configuration, true, &fakeRunner{})
	application.NewCoreSynchronizer = func(config.Config) (CoreSynchronizer, error) { return synchronizer, nil }

	if exitCode := application.Run(context.Background()); exitCode != 0 {
		t.Fatalf("Run() = %d, want Core failure to remain nonfatal", exitCode)
	}
	if synchronizer.calls != 1 {
		t.Fatalf("Sync() calls = %d, want 1", synchronizer.calls)
	}
	var report inventory.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Core == nil || report.Core.Heartbeat.Status != "failed" || len(report.Warnings) != 1 {
		t.Fatalf("report = %#v", report)
	}
	wantEvents := []string{"config_loaded", "collection_started", "core_sync_started", "inventory_warning", "core_sync_completed", "collection_completed"}
	if got := logEvents(decodeLogs(t, stderr.String())); !reflect.DeepEqual(got, wantEvents) {
		t.Fatalf("events = %#v, want %#v", got, wantEvents)
	}
}

func TestRunCoreClientInitializationFailureIsNonfatal(t *testing.T) {
	t.Parallel()

	configuration := config.Defaults()
	configuration.Core.Enabled = true
	configuration.Core.BaseURL = "https://core.example"
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	application := testApp(&stdout, &stderr, configuration, true, &fakeRunner{})
	application.NewCoreSynchronizer = func(config.Config) (CoreSynchronizer, error) {
		return nil, errors.New("invalid CA bundle")
	}
	if exitCode := application.Run(context.Background()); exitCode != 0 {
		t.Fatalf("Run() = %d, want 0", exitCode)
	}
	var report inventory.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Core == nil || report.Core.Registration.Status != "failed" || report.Core.Heartbeat.Status != "skipped" || len(report.Warnings) != 1 || report.Warnings[0].Source != "core.client" {
		t.Fatalf("report = %#v", report)
	}
}

func TestRunCoreDisabledNeverConstructsSynchronizer(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	application := testApp(&stdout, &stderr, config.Defaults(), true, &fakeRunner{})
	application.NewCoreSynchronizer = func(config.Config) (CoreSynchronizer, error) {
		t.Fatal("Core synchronizer constructed while disabled")
		return nil, nil
	}
	if exitCode := application.Run(context.Background()); exitCode != 0 {
		t.Fatalf("Run() = %d, want 0", exitCode)
	}
	var report inventory.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Core == nil || report.Core.Enabled || report.Core.Registration.Status != "disabled" {
		t.Fatalf("Core = %#v", report.Core)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("disk full")
}

func testApp(stdout, stderr interface{ Write([]byte) (int, error) }, configuration config.Config, loaded bool, runner Runner) App {
	return App{
		Stdout: stdout,
		Stderr: stderr,
		LoadConfig: func() (config.Config, bool, error) {
			return configuration, loaded, nil
		},
		NewRunner:  func(config.Config) Runner { return runner },
		Now:        time.Now,
		ConfigPath: config.DefaultPath,
	}
}

func decodeLogs(t *testing.T, output string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	logs := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("invalid JSON log %q: %v", line, err)
		}
		logs = append(logs, entry)
	}
	return logs
}

func logEvents(logs []map[string]any) []string {
	events := make([]string, 0, len(logs))
	for _, entry := range logs {
		events = append(events, entry["event"].(string))
	}
	return events
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

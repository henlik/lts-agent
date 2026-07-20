package agent

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/henlik/lts-agent/internal/inventory"
	"github.com/henlik/lts-agent/internal/system"
)

type fakeSystemCollector struct {
	result   system.Result
	warnings []inventory.Warning
}

func (f fakeSystemCollector) Collect(context.Context) (system.Result, []inventory.Warning) {
	return f.result, f.warnings
}

type fakeLBICollector struct {
	metadata inventory.LBI
	warnings []inventory.Warning
}

type fakeAssignmentCollector struct {
	assignment inventory.Assignment
	warnings   []inventory.Warning
}

func (f fakeAssignmentCollector) Collect() (inventory.Assignment, []inventory.Warning) {
	return f.assignment, f.warnings
}

func (f fakeLBICollector) Collect() (inventory.LBI, []inventory.Warning) {
	return f.metadata, f.warnings
}

type fakeCheckCollector struct {
	checks   inventory.Checks
	warnings []inventory.Warning
}

func (f fakeCheckCollector) Collect(context.Context) (inventory.Checks, []inventory.Warning) {
	return f.checks, f.warnings
}

func TestWriteJSONStableOutput(t *testing.T) {
	t.Parallel()

	runner := New(
		fakeSystemCollector{result: system.Result{
			Hostname: "lts-app-001",
			System: inventory.System{
				OS:           "Ubuntu 24.04.4 LTS",
				Kernel:       "6.8.0-136-generic",
				Architecture: "x86_64",
				Timezone:     "Africa/Lubumbashi",
			},
		}},
		fakeLBICollector{metadata: inventory.LBI{
			Available:  true,
			Name:       "LTS Base Image",
			Short:      "LBI",
			Version:    "1.0",
			Build:      "001",
			BaseOS:     "Ubuntu 24.04.4 LTS",
			Maintainer: "Likone Technologies",
		}},
		fakeAssignmentCollector{assignment: inventory.Assignment{
			Available:     true,
			SchemaVersion: 1,
			Roles:         []string{"application-node"},
			Capabilities:  []string{"docker", "postgresql"},
		}},
		fakeCheckCollector{checks: inventory.Checks{
			Validation: inventory.Check{
				Available:  true,
				Status:     "passed",
				ExitCode:   intPointer(0),
				DurationMS: 125,
				Output:     "Validation: PASS",
			},
			Health: inventory.Check{
				Available:  true,
				Status:     "healthy",
				ExitCode:   intPointer(0),
				DurationMS: 340,
				Output:     "0 warnings, 0 criticals",
			},
		}},
	)

	var output bytes.Buffer
	if err := runner.WriteJSON(context.Background(), &output); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	want := `{
  "agent": {
    "version": "0.7.1"
  },
  "node": {
    "hostname": "lts-app-001"
  },
  "lbi": {
    "available": true,
    "name": "LTS Base Image",
    "short": "LBI",
    "version": "1.0",
    "build": "001",
    "base_os": "Ubuntu 24.04.4 LTS",
    "maintainer": "Likone Technologies"
  },
  "system": {
    "os": "Ubuntu 24.04.4 LTS",
    "kernel": "6.8.0-136-generic",
    "architecture": "x86_64",
    "timezone": "Africa/Lubumbashi"
  },
  "assignment": {
    "available": true,
    "schema_version": 1,
    "roles": [
      "application-node"
    ],
    "capabilities": [
      "docker",
      "postgresql"
    ]
  },
  "checks": {
    "validation": {
      "available": true,
      "status": "passed",
      "exit_code": 0,
      "duration_ms": 125,
      "output": "Validation: PASS",
      "truncated": false
    },
    "health": {
      "available": true,
      "status": "healthy",
      "exit_code": 0,
      "duration_ms": 340,
      "output": "0 warnings, 0 criticals",
      "truncated": false
    }
  }
}
`
	if output.String() != want {
		t.Fatalf("WriteJSON() output:\n%s\nwant:\n%s", output.String(), want)
	}
}

func TestCollectAggregatesWarningsAndUnavailableLBI(t *testing.T) {
	t.Parallel()

	runner := New(
		fakeSystemCollector{warnings: []inventory.Warning{{Source: "kernel", Message: "unavailable"}}},
		fakeLBICollector{
			metadata: inventory.LBI{Available: false},
			warnings: []inventory.Warning{{Source: "lbi", Message: "missing"}},
		},
		fakeAssignmentCollector{
			warnings: []inventory.Warning{{Source: "assignment", Message: "missing"}},
		},
		fakeCheckCollector{
			warnings: []inventory.Warning{{Source: "checks.validation", Message: "missing"}},
		},
	)

	report := runner.Collect(context.Background())
	if report.Agent.Version != Version || report.LBI.Available {
		t.Fatalf("report = %#v, want version %s and unavailable LBI", report, Version)
	}
	if report.Assignment.Roles == nil || report.Assignment.Capabilities == nil {
		t.Fatalf("assignment arrays = %#v, %#v; want non-nil", report.Assignment.Roles, report.Assignment.Capabilities)
	}
	if len(report.Warnings) != 4 || report.Warnings[0].Source != "kernel" || report.Warnings[1].Source != "lbi" || report.Warnings[2].Source != "assignment" || report.Warnings[3].Source != "checks.validation" {
		t.Fatalf("warnings = %#v, want deterministic aggregation", report.Warnings)
	}
}

func TestWriteJSONUnavailableAssignmentUsesEmptyArrays(t *testing.T) {
	t.Parallel()

	runner := New(fakeSystemCollector{}, fakeLBICollector{}, fakeAssignmentCollector{}, fakeCheckCollector{})
	var output bytes.Buffer
	if err := runner.WriteJSON(context.Background(), &output); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	wantAssignment := `"assignment": {
    "available": false,
    "roles": [],
    "capabilities": []
  }`
	if !strings.Contains(output.String(), wantAssignment) {
		t.Fatalf("WriteJSON() output:\n%s\nwant unavailable assignment with empty arrays", output.String())
	}
	if strings.Contains(output.String(), "schema_version") {
		t.Fatalf("WriteJSON() output contains schema_version for unavailable assignment:\n%s", output.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("disk full")
}

func TestWriteJSONReturnsWriterFailure(t *testing.T) {
	t.Parallel()

	runner := New(fakeSystemCollector{}, fakeLBICollector{}, fakeAssignmentCollector{}, fakeCheckCollector{})
	err := runner.WriteJSON(context.Background(), failingWriter{})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("WriteJSON() error = %v, want disk full", err)
	}
}

func TestWriteReportJSONDoesNotCollectAgain(t *testing.T) {
	t.Parallel()

	collector := &countingSystemCollector{}
	runner := New(collector, fakeLBICollector{}, fakeAssignmentCollector{}, fakeCheckCollector{})
	report := runner.Collect(context.Background())
	if collector.calls != 1 {
		t.Fatalf("calls after Collect() = %d, want 1", collector.calls)
	}

	var output bytes.Buffer
	if err := WriteReportJSON(report, &output); err != nil {
		t.Fatalf("WriteReportJSON() error = %v", err)
	}
	if collector.calls != 1 {
		t.Fatalf("calls after WriteReportJSON() = %d, want no recollection", collector.calls)
	}
}

type countingSystemCollector struct {
	calls int
}

func (c *countingSystemCollector) Collect(context.Context) (system.Result, []inventory.Warning) {
	c.calls++
	return system.Result{}, nil
}

func intPointer(value int) *int {
	return &value
}

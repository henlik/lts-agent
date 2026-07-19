package check

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStatusMappings(t *testing.T) {
	t.Parallel()

	validationCases := map[int]string{0: "passed", 1: "failed", 17: "failed"}
	for exitCode, want := range validationCases {
		status, err := validationStatus(exitCode)
		if err != nil || status != want {
			t.Errorf("validationStatus(%d) = %q, %v; want %q, nil", exitCode, status, err, want)
		}
	}

	healthCases := map[int]string{0: "healthy", 1: "degraded", 2: "critical"}
	for exitCode, want := range healthCases {
		status, err := healthStatus(exitCode)
		if err != nil || status != want {
			t.Errorf("healthStatus(%d) = %q, %v; want %q, nil", exitCode, status, err, want)
		}
	}
	if status, err := healthStatus(3); status != "error" || err == nil {
		t.Errorf("healthStatus(3) = %q, %v; want error status and error", status, err)
	}
}

func TestCollectorRunsSequentiallyWithIndependentDeadlines(t *testing.T) {
	t.Parallel()

	var paths []string
	collector := NewCollectorWithDependencies(Dependencies{
		Execute: func(ctx context.Context, path string, limit int) Execution {
			paths = append(paths, path)
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("check context has no deadline")
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > 100*time.Millisecond {
				t.Fatalf("deadline remaining = %s, want within 100ms", remaining)
			}
			if limit != 1234 {
				t.Fatalf("output limit = %d, want 1234", limit)
			}
			code := 0
			return Execution{Started: true, ExitCode: &code}
		},
		Now:         time.Now,
		Timeout:     100 * time.Millisecond,
		OutputLimit: 1234,
	})

	checks, warnings := collector.Collect(context.Background())
	if want := []string{ValidationPath, HealthPath}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
	if checks.Validation.Status != "passed" || checks.Health.Status != "healthy" || len(warnings) != 0 {
		t.Fatalf("checks = %#v, warnings = %#v", checks, warnings)
	}
}

func TestCollectorUsesConfiguredOptions(t *testing.T) {
	t.Parallel()

	var paths []string
	collector := NewCollectorWithDependencies(Dependencies{
		Execute: func(ctx context.Context, path string, limit int) Execution {
			paths = append(paths, path)
			if limit != 8192 {
				t.Fatalf("limit = %d, want 8192", limit)
			}
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 2*time.Second || time.Until(deadline) <= 0 {
				t.Fatalf("deadline = %v, want within 2 seconds", deadline)
			}
			code := 0
			return Execution{Started: true, ExitCode: &code}
		},
		Now:            time.Now,
		ValidationPath: "/custom/validation",
		HealthPath:     "/custom/health",
		Timeout:        2 * time.Second,
		OutputLimit:    8192,
	})

	_, warnings := collector.Collect(context.Background())
	if want := []string{"/custom/validation", "/custom/health"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", warnings)
	}
}

func TestCollectorMapsValidNonzeroOutcomesWithoutWarnings(t *testing.T) {
	t.Parallel()

	collector := fakeCollector(func(path string) Execution {
		code := 5
		if path == HealthPath {
			code = 2
		}
		return Execution{Started: true, ExitCode: &code, Output: "details"}
	})

	checks, warnings := collector.Collect(context.Background())
	if checks.Validation.Status != "failed" || checks.Health.Status != "critical" {
		t.Fatalf("checks = %#v, want failed and critical", checks)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", warnings)
	}
}

func TestCollectorWarnsAboutUnsupportedHealthExit(t *testing.T) {
	t.Parallel()

	collector := fakeCollector(func(path string) Execution {
		code := 0
		if path == HealthPath {
			code = 9
		}
		return Execution{Started: true, ExitCode: &code}
	})

	checks, warnings := collector.Collect(context.Background())
	if checks.Health.Status != "error" || checks.Health.ExitCode == nil || *checks.Health.ExitCode != 9 {
		t.Fatalf("Health = %#v, want error with exit code 9", checks.Health)
	}
	if len(warnings) != 1 || warnings[0].Source != "checks.health" || !strings.Contains(warnings[0].Message, "unsupported health exit code 9") {
		t.Fatalf("warnings = %#v, want unsupported health warning", warnings)
	}
}

func TestCollectorReportsUnavailableChecks(t *testing.T) {
	t.Parallel()

	collector := fakeCollector(func(path string) Execution {
		return Execution{Err: errors.New("permission denied")}
	})
	checks, warnings := collector.Collect(context.Background())

	if checks.Validation.Available || checks.Validation.Status != "unavailable" || checks.Validation.ExitCode != nil {
		t.Fatalf("Validation = %#v, want unavailable", checks.Validation)
	}
	if checks.Health.Available || checks.Health.Status != "unavailable" || checks.Health.ExitCode != nil {
		t.Fatalf("Health = %#v, want unavailable", checks.Health)
	}
	if len(warnings) != 2 || warnings[0].Source != "checks.validation" || warnings[1].Source != "checks.health" {
		t.Fatalf("warnings = %#v, want deterministic check warnings", warnings)
	}
}

func TestCollectorTimesOutEachCheck(t *testing.T) {
	t.Parallel()

	collector := NewCollectorWithDependencies(Dependencies{
		Execute: func(ctx context.Context, _ string, _ int) Execution {
			<-ctx.Done()
			return Execution{Started: true, Err: ctx.Err()}
		},
		Now:         time.Now,
		Timeout:     5 * time.Millisecond,
		OutputLimit: MaxOutputBytes,
	})
	checks, warnings := collector.Collect(context.Background())

	if !checks.Validation.Available || checks.Validation.Status != "timeout" || checks.Validation.ExitCode != nil {
		t.Fatalf("Validation = %#v, want available timeout", checks.Validation)
	}
	if !checks.Health.Available || checks.Health.Status != "timeout" || len(warnings) != 2 {
		t.Fatalf("Health = %#v, warnings = %#v; want second timeout", checks.Health, warnings)
	}
}

func TestCollectorReportsCallerCancellationAsError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	collector := fakeCollector(func(string) Execution {
		return Execution{Err: context.Canceled}
	})
	checks, warnings := collector.Collect(ctx)

	if checks.Validation.Status != "error" || checks.Health.Status != "error" || len(warnings) != 2 {
		t.Fatalf("checks = %#v, warnings = %#v; want cancellation errors", checks, warnings)
	}
}

func TestExecuteCapturesCombinedOutputAndExitCode(t *testing.T) {
	t.Parallel()

	path := writeScript(t, "#!/bin/sh\nprintf 'standard output\\n'\nprintf 'standard error\\n' >&2\nexit 2\n", 0o755)
	result := execute(context.Background(), path, MaxOutputBytes)
	if !result.Started || result.ExitCode == nil || *result.ExitCode != 2 {
		t.Fatalf("execution = %#v, want started exit 2", result)
	}
	if !strings.Contains(result.Output, "standard output") || !strings.Contains(result.Output, "standard error") {
		t.Fatalf("Output = %q, want combined stdout and stderr", result.Output)
	}
}

func TestExecuteHandlesEmptyOutputAndTruncation(t *testing.T) {
	t.Parallel()

	emptyPath := writeScript(t, "#!/bin/sh\nexit 0\n", 0o755)
	empty := execute(context.Background(), emptyPath, MaxOutputBytes)
	if empty.Output != "" || empty.Truncated {
		t.Fatalf("empty execution = %#v", empty)
	}

	outputPath := writeScript(t, "#!/bin/sh\nprintf 'abcdefghij'\n", 0o755)
	truncated := execute(context.Background(), outputPath, 4)
	if truncated.Output != "abcd" || !truncated.Truncated {
		t.Fatalf("truncated execution = %#v, want abcd and truncated", truncated)
	}
}

func TestExecuteReportsMissingAndNonExecutableScripts(t *testing.T) {
	t.Parallel()

	missing := execute(context.Background(), filepath.Join(t.TempDir(), "missing.sh"), MaxOutputBytes)
	if missing.Started || missing.Err == nil {
		t.Fatalf("missing execution = %#v, want start error", missing)
	}

	nonExecutablePath := writeScript(t, "#!/bin/sh\nexit 0\n", 0o644)
	nonExecutable := execute(context.Background(), nonExecutablePath, MaxOutputBytes)
	if nonExecutable.Started || nonExecutable.Err == nil {
		t.Fatalf("non-executable execution = %#v, want start error", nonExecutable)
	}
}

func TestExecuteHandlesTimeoutAndSignalTermination(t *testing.T) {
	t.Parallel()

	timeoutPath := writeScript(t, "#!/bin/sh\nexec sleep 5\n", 0o755)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	timedOut := execute(ctx, timeoutPath, MaxOutputBytes)
	if !timedOut.Started || timedOut.ExitCode != nil || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("timed-out execution = %#v, context error = %v", timedOut, ctx.Err())
	}

	signalPath := writeScript(t, "#!/bin/sh\nkill -TERM $$\n", 0o755)
	signalled := execute(context.Background(), signalPath, MaxOutputBytes)
	if !signalled.Started || signalled.ExitCode != nil || signalled.Err == nil {
		t.Fatalf("signalled execution = %#v, want abnormal termination", signalled)
	}
}

func fakeCollector(result func(string) Execution) *Collector {
	return NewCollectorWithDependencies(Dependencies{
		Execute:     func(_ context.Context, path string, _ int) Execution { return result(path) },
		Now:         time.Now,
		Timeout:     time.Second,
		OutputLimit: MaxOutputBytes,
	})
}

func writeScript(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "check.sh")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

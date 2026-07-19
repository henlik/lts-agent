// Package check executes and normalizes the existing LBI validation and health
// scripts. It reports their results but never modifies the scripts or host.
package check

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/henlik/lts-agent/internal/inventory"
)

const (
	ValidationPath = "/opt/lts/scripts/lbi-validate.sh"
	HealthPath     = "/opt/lts/scripts/health-check.sh"
	DefaultTimeout = 30 * time.Second
	MaxOutputBytes = 64 * 1024
)

// Execution is the platform-level outcome of attempting one process.
type Execution struct {
	Started   bool
	ExitCode  *int
	Output    string
	Truncated bool
	Err       error
}

// ExecuteFunc is injectable so status behavior can be tested without requiring
// the LBI scripts on the development host.
type ExecuteFunc func(context.Context, string, int) Execution

// Dependencies controls process execution, deadlines, and duration measurement.
type Dependencies struct {
	Execute        ExecuteFunc
	Now            func() time.Time
	ValidationPath string
	HealthPath     string
	Timeout        time.Duration
	OutputLimit    int
}

// Options contains administrator-configurable check behavior.
type Options struct {
	ValidationPath string
	HealthPath     string
	Timeout        time.Duration
	OutputLimit    int
}

// DefaultOptions returns the v0.3-compatible check behavior.
func DefaultOptions() Options {
	return Options{
		ValidationPath: ValidationPath,
		HealthPath:     HealthPath,
		Timeout:        DefaultTimeout,
		OutputLimit:    MaxOutputBytes,
	}
}

// Collector runs validation followed by health and normalizes both results.
type Collector struct {
	deps Dependencies
}

// NewCollector creates a collector using direct, unprivileged process execution.
func NewCollector() *Collector {
	return NewCollectorWithOptions(DefaultOptions())
}

// NewCollectorWithOptions applies validated runtime configuration.
func NewCollectorWithOptions(options Options) *Collector {
	return NewCollectorWithDependencies(Dependencies{
		Execute:        execute,
		Now:            time.Now,
		ValidationPath: options.ValidationPath,
		HealthPath:     options.HealthPath,
		Timeout:        options.Timeout,
		OutputLimit:    options.OutputLimit,
	})
}

// NewCollectorWithDependencies creates a collector with deterministic boundaries.
func NewCollectorWithDependencies(deps Dependencies) *Collector {
	defaults := DefaultOptions()
	if deps.ValidationPath == "" {
		deps.ValidationPath = defaults.ValidationPath
	}
	if deps.HealthPath == "" {
		deps.HealthPath = defaults.HealthPath
	}
	return &Collector{deps: deps}
}

// Collect deliberately runs sequentially. These scripts inspect overlapping
// system state, and avoiding concurrent execution keeps load and output ordering
// predictable.
func (c *Collector) Collect(ctx context.Context) (inventory.Checks, []inventory.Warning) {
	validation, validationWarnings := c.run(ctx, "validation", c.deps.ValidationPath, validationStatus)
	health, healthWarnings := c.run(ctx, "health", c.deps.HealthPath, healthStatus)

	warnings := make([]inventory.Warning, 0, len(validationWarnings)+len(healthWarnings))
	warnings = append(warnings, validationWarnings...)
	warnings = append(warnings, healthWarnings...)
	return inventory.Checks{Validation: validation, Health: health}, warnings
}

type statusMapper func(int) (string, error)

func (c *Collector) run(ctx context.Context, name, path string, mapStatus statusMapper) (inventory.Check, []inventory.Warning) {
	startedAt := c.deps.Now()
	checkCtx, cancel := context.WithTimeout(ctx, c.deps.Timeout)
	execution := c.deps.Execute(checkCtx, path, c.deps.OutputLimit)
	cancel()

	result := inventory.Check{
		Available:  execution.Started,
		DurationMS: c.deps.Now().Sub(startedAt).Milliseconds(),
		Output:     execution.Output,
		Truncated:  execution.Truncated,
	}
	source := "checks." + name

	if ctx.Err() != nil {
		result.Status = "error"
		return result, []inventory.Warning{{Source: source, Message: fmt.Sprintf("check cancelled: %v", ctx.Err())}}
	}
	if errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
		result.Status = "timeout"
		return result, []inventory.Warning{{Source: source, Message: fmt.Sprintf("check timed out after %s", c.deps.Timeout)}}
	}
	if !execution.Started {
		result.Status = "unavailable"
		return result, []inventory.Warning{{Source: source, Message: fmt.Sprintf("execute %s: %v", path, execution.Err)}}
	}
	if execution.ExitCode == nil {
		result.Status = "error"
		return result, []inventory.Warning{{Source: source, Message: fmt.Sprintf("check terminated without a normal exit: %v", execution.Err)}}
	}

	result.ExitCode = execution.ExitCode
	status, err := mapStatus(*execution.ExitCode)
	result.Status = status
	if err != nil {
		return result, []inventory.Warning{{Source: source, Message: err.Error()}}
	}
	return result, nil
}

func validationStatus(exitCode int) (string, error) {
	if exitCode == 0 {
		return "passed", nil
	}
	return "failed", nil
}

func healthStatus(exitCode int) (string, error) {
	switch exitCode {
	case 0:
		return "healthy", nil
	case 1:
		return "degraded", nil
	case 2:
		return "critical", nil
	default:
		return "error", fmt.Errorf("unsupported health exit code %d", exitCode)
	}
}

func execute(ctx context.Context, path string, outputLimit int) Execution {
	command := exec.CommandContext(ctx, path)
	output := newLimitedBuffer(outputLimit)
	// A single synchronized writer retains the relative order provided by
	// os/exec while bounding combined stdout and stderr memory.
	command.Stdout = output
	command.Stderr = output

	if err := command.Start(); err != nil {
		return Execution{Output: output.String(), Truncated: output.Truncated(), Err: err}
	}

	err := command.Wait()
	var exitCode *int
	if code := command.ProcessState.ExitCode(); code >= 0 {
		exitCode = &code
	}
	return Execution{
		Started:   true,
		ExitCode:  exitCode,
		Output:    output.String(),
		Truncated: output.Truncated(),
		Err:       err,
	}
}

type limitedBuffer struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{data: make([]byte, 0, limit), limit: limit}
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	written := len(data)
	remaining := b.limit - len(b.data)
	if remaining <= 0 {
		if written > 0 {
			b.truncated = true
		}
		return written, nil
	}
	if len(data) > remaining {
		b.data = append(b.data, data[:remaining]...)
		b.truncated = true
		return written, nil
	}
	b.data = append(b.data, data...)
	return written, nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(strings.ToValidUTF8(string(b.data), "�"))
}

func (b *limitedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

// Package app owns the command lifecycle around configuration, logging,
// collection, and output. Keeping this orchestration separate makes the stdout
// and stderr contracts testable without spawning the binary.
package app

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/henlik/lts-agent/internal/agent"
	"github.com/henlik/lts-agent/internal/config"
	"github.com/henlik/lts-agent/internal/inventory"
)

// Runner is the collection behavior required from the agent.
type Runner interface {
	Collect(context.Context) inventory.Report
}

// ConfigLoader returns effective configuration and whether it came from a file.
type ConfigLoader func() (config.Config, bool, error)

// RunnerFactory applies effective configuration to the agent's collectors.
type RunnerFactory func(config.Config) Runner

// CoreSynchronizer performs the optional registration, heartbeat, and desired-
// state workflow.
type CoreSynchronizer interface {
	Sync(context.Context, inventory.Report) (*inventory.Core, *inventory.DesiredState, []inventory.Warning)
}

// CoreSynchronizerFactory constructs the configured HTTPS workflow.
type CoreSynchronizerFactory func(config.Config) (CoreSynchronizer, error)

// App contains the command's injectable lifecycle dependencies.
type App struct {
	Stdout              io.Writer
	Stderr              io.Writer
	LoadConfig          ConfigLoader
	NewRunner           RunnerFactory
	NewCoreSynchronizer CoreSynchronizerFactory
	Now                 func() time.Time
	ConfigPath          string
}

// Run emits complete inventory on success and returns a process exit code.
func (a App) Run(ctx context.Context) int {
	configuration, loaded, err := a.LoadConfig()
	if err != nil {
		logger := newLogger(a.Stderr, slog.LevelInfo)
		logger.Error(
			"configuration is invalid",
			"event", "config_invalid",
			"agent_version", agent.Version,
			"path", a.ConfigPath,
			"detail", err.Error(),
		)
		return 1
	}

	level, _ := config.LogLevel(configuration.Logging.Level)
	logger := newLogger(a.Stderr, level).With("agent_version", agent.Version)
	if loaded {
		logger.Info("configuration loaded", "event", "config_loaded", "path", a.ConfigPath, "schema_version", configuration.SchemaVersion)
	} else {
		logger.Info("using default configuration", "event", "config_defaults_used", "path", a.ConfigPath)
	}

	startedAt := a.Now()
	logger.Info("inventory collection started", "event", "collection_started")
	report := a.NewRunner(configuration).Collect(ctx)
	ensureDesiredState(&report)
	if configuration.Core.Enabled {
		logger.Info("Core synchronization started", "event", "core_sync_started")
		synchronizer, err := a.NewCoreSynchronizer(configuration)
		if err != nil {
			report.Core = &inventory.Core{
				Enabled:      true,
				Registration: inventory.Operation{Status: "failed"},
				Heartbeat:    inventory.Operation{Status: "skipped"},
				DesiredState: inventory.Operation{Status: "skipped"},
			}
			report.Warnings = append(report.Warnings, inventory.Warning{Source: "core.client", Message: err.Error()})
		} else {
			var coreWarnings []inventory.Warning
			report.Core, report.DesiredState, coreWarnings = synchronizer.Sync(ctx, report)
			ensureDesiredState(&report)
			report.Warnings = append(report.Warnings, coreWarnings...)
		}
	} else {
		report.Core = &inventory.Core{
			Registration: inventory.Operation{Status: "disabled"},
			Heartbeat:    inventory.Operation{Status: "disabled"},
			DesiredState: inventory.Operation{Status: "disabled"},
		}
	}
	for _, warning := range report.Warnings {
		logger.Warn("inventory collection warning", "event", "inventory_warning", "source", warning.Source, "detail", warning.Message)
	}
	if configuration.Core.Enabled {
		logger.Info(
			"Core synchronization completed",
			"event", "core_sync_completed",
			"registered", report.Core.Registered,
			"registration_status", report.Core.Registration.Status,
			"heartbeat_status", report.Core.Heartbeat.Status,
			"desired_state_status", report.Core.DesiredState.Status,
		)
	}

	if err := agent.WriteReportJSON(report, a.Stdout); err != nil {
		logger.Error("inventory output failed", "event", "inventory_write_failed", "detail", err.Error())
		return 1
	}

	logger.Info(
		"inventory collection completed",
		"event", "collection_completed",
		"duration_ms", a.Now().Sub(startedAt).Milliseconds(),
		"warning_count", len(report.Warnings),
		"validation_status", report.Checks.Validation.Status,
		"health_status", report.Checks.Health.Status,
		"core_enabled", report.Core.Enabled,
		"core_registered", report.Core.Registered,
		"desired_state_available", report.DesiredState.Available,
	)
	return 0
}

func ensureDesiredState(report *inventory.Report) {
	if report.DesiredState == nil {
		report.DesiredState = &inventory.DesiredState{}
	}
	if report.DesiredState.Roles == nil {
		report.DesiredState.Roles = []string{}
	}
	if report.DesiredState.Capabilities == nil {
		report.DesiredState.Capabilities = []string{}
	}
}

func newLogger(writer io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level}))
}

// Command lts-agent prints local node inventory as JSON.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/henlik/lts-agent/internal/agent"
	"github.com/henlik/lts-agent/internal/app"
	"github.com/henlik/lts-agent/internal/check"
	"github.com/henlik/lts-agent/internal/config"
	coreclient "github.com/henlik/lts-agent/internal/core"
	"github.com/henlik/lts-agent/internal/coresync"
	"github.com/henlik/lts-agent/internal/lbi"
	"github.com/henlik/lts-agent/internal/role"
	"github.com/henlik/lts-agent/internal/system"
)

func main() {
	// Signal-aware context is useful today for the uname fallback and establishes
	// the cancellation contract needed by future collectors that may block.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	application := app.App{
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		LoadConfig: config.Load,
		NewRunner: func(configuration config.Config) app.Runner {
			checkOptions := check.Options{
				ValidationPath: configuration.Checks.ValidationPath,
				HealthPath:     configuration.Checks.HealthPath,
				Timeout:        time.Duration(configuration.Checks.TimeoutSeconds) * time.Second,
				OutputLimit:    configuration.Checks.MaxOutputBytes,
			}
			return agent.New(system.NewCollector(), lbi.NewCollector(), role.NewCollector(), check.NewCollectorWithOptions(checkOptions))
		},
		NewCoreSynchronizer: func(configuration config.Config) (app.CoreSynchronizer, error) {
			client, err := coreclient.New(coreclient.Options{
				BaseURL:   configuration.Core.BaseURL,
				Timeout:   time.Duration(configuration.Core.RequestTimeoutSeconds) * time.Second,
				CAFile:    configuration.Core.CAFile,
				UserAgent: "lts-agent/" + agent.Version,
			})
			if err != nil {
				return nil, err
			}
			return coresync.New(client, coresync.Options{
				Enabled:             true,
				AgentVersion:        agent.Version,
				EnrollmentTokenFile: configuration.Core.EnrollmentTokenFile,
				StateFile:           configuration.Core.StateFile,
			}), nil
		},
		Now:        time.Now,
		ConfigPath: config.DefaultPath,
	}
	if exitCode := application.Run(ctx); exitCode != 0 {
		os.Exit(exitCode)
	}
}

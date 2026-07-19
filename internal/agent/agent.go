// Package agent coordinates inventory collectors and serializes their result.
// It intentionally contains no registration, networking, or job execution.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/henlik/lts-agent/internal/inventory"
	"github.com/henlik/lts-agent/internal/system"
)

// Version is the semantic version reported by this build of lts-agent.
const Version = "0.7.0"

// SystemCollector is the system inventory boundary used by Agent.
type SystemCollector interface {
	Collect(context.Context) (system.Result, []inventory.Warning)
}

// LBICollector is the base-image metadata boundary used by Agent.
type LBICollector interface {
	Collect() (inventory.LBI, []inventory.Warning)
}

// AssignmentCollector is the role and capability inventory boundary used by Agent.
type AssignmentCollector interface {
	Collect() (inventory.Assignment, []inventory.Warning)
}

// CheckCollector is the local validation and health boundary used by Agent.
type CheckCollector interface {
	Collect(context.Context) (inventory.Checks, []inventory.Warning)
}

// Agent assembles independently collected data into the stable report schema.
type Agent struct {
	system     SystemCollector
	lbi        LBICollector
	assignment AssignmentCollector
	checks     CheckCollector
}

// New creates an inventory agent with explicit collectors.
func New(
	systemCollector SystemCollector,
	lbiCollector LBICollector,
	assignmentCollector AssignmentCollector,
	checkCollector CheckCollector,
) *Agent {
	return &Agent{
		system:     systemCollector,
		lbi:        lbiCollector,
		assignment: assignmentCollector,
		checks:     checkCollector,
	}
}

// Collect gathers all local inventory. Individual collector warnings remain in
// the report instead of preventing independent sources from being returned.
func (a *Agent) Collect(ctx context.Context) inventory.Report {
	systemResult, systemWarnings := a.system.Collect(ctx)
	lbiResult, lbiWarnings := a.lbi.Collect()
	assignmentResult, assignmentWarnings := a.assignment.Collect()
	checkResults, checkWarnings := a.checks.Collect(ctx)
	// Enforce the public [] contract even when a custom collector returns a zero
	// value. JSON null would be ambiguous for downstream consumers.
	if assignmentResult.Roles == nil {
		assignmentResult.Roles = []string{}
	}
	if assignmentResult.Capabilities == nil {
		assignmentResult.Capabilities = []string{}
	}

	warnings := make([]inventory.Warning, 0, len(systemWarnings)+len(lbiWarnings)+len(assignmentWarnings)+len(checkWarnings))
	warnings = append(warnings, systemWarnings...)
	warnings = append(warnings, lbiWarnings...)
	warnings = append(warnings, assignmentWarnings...)
	warnings = append(warnings, checkWarnings...)

	return inventory.Report{
		Agent:      inventory.Agent{Version: Version},
		Node:       inventory.Node{Hostname: systemResult.Hostname},
		LBI:        lbiResult,
		System:     systemResult.System,
		Assignment: assignmentResult,
		Checks:     checkResults,
		Warnings:   warnings,
	}
}

// WriteJSON emits a human-readable JSON document followed by one newline. A
// write or encoding error is fatal because callers would otherwise receive a
// truncated document that could be mistaken for valid inventory.
func (a *Agent) WriteJSON(ctx context.Context, writer io.Writer) error {
	return WriteReportJSON(a.Collect(ctx), writer)
}

// WriteReportJSON serializes an already-collected report. Application startup
// uses this function so it can log report warnings without running collectors a
// second time.
func WriteReportJSON(report inventory.Report, writer io.Writer) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("encode inventory JSON: %w", err)
	}
	return nil
}

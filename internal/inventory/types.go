// Package inventory defines the stable JSON document emitted by lts-agent.
// Keeping the wire shape separate from collection logic prevents operating-
// system details from leaking into the public output contract.
package inventory

// Report is the complete inventory document emitted by the current agent.
type Report struct {
	Agent      Agent      `json:"agent"`
	Node       Node       `json:"node"`
	LBI        LBI        `json:"lbi"`
	System     System     `json:"system"`
	Assignment Assignment `json:"assignment"`
	Checks     Checks     `json:"checks"`
	Core       *Core      `json:"core,omitempty"`
	Warnings   []Warning  `json:"warnings,omitempty"`
}

// Agent identifies the software that produced a report.
type Agent struct {
	Version string `json:"version"`
}

// Node contains identity that belongs to the current machine.
type Node struct {
	Hostname string `json:"hostname"`
}

// LBI describes the LTS Base Image when /etc/lbi-release is available.
// Available is always emitted so consumers do not have to infer availability
// from empty metadata fields.
type LBI struct {
	Available  bool   `json:"available"`
	Name       string `json:"name,omitempty"`
	Short      string `json:"short,omitempty"`
	Version    string `json:"version,omitempty"`
	Build      string `json:"build,omitempty"`
	BaseOS     string `json:"base_os,omitempty"`
	Maintainer string `json:"maintainer,omitempty"`
}

// System contains portable operating-system inventory values. Architecture is
// normalized to Linux conventions (for example, x86_64 instead of amd64).
type System struct {
	OS           string `json:"os"`
	Kernel       string `json:"kernel"`
	Architecture string `json:"architecture"`
	Timezone     string `json:"timezone"`
}

// Assignment describes the roles and capabilities desired for this node.
// Roles and Capabilities are always emitted as JSON arrays, including when the
// assignment source is unavailable or contains no entries.
type Assignment struct {
	Available     bool     `json:"available"`
	SchemaVersion int      `json:"schema_version,omitempty"`
	Roles         []string `json:"roles"`
	Capabilities  []string `json:"capabilities"`
}

// Checks groups runtime validation and health results. Both entries are always
// emitted so consumers can distinguish unavailable checks from absent data.
type Checks struct {
	Validation Check `json:"validation"`
	Health     Check `json:"health"`
}

// Check is the normalized result of one local LBI script. ExitCode is omitted
// when the process never returned a normal exit status.
type Check struct {
	Available  bool   `json:"available"`
	Status     string `json:"status"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Output     string `json:"output"`
	Truncated  bool   `json:"truncated"`
}

// Core summarizes one opt-in synchronization attempt without exposing
// credentials or transport error details.
type Core struct {
	Enabled      bool      `json:"enabled"`
	Registered   bool      `json:"registered"`
	NodeID       string    `json:"node_id,omitempty"`
	Registration Operation `json:"registration"`
	Heartbeat    Operation `json:"heartbeat"`
}

// Operation is the stable state of one Core workflow stage.
type Operation struct {
	Attempted bool   `json:"attempted"`
	Status    string `json:"status"`
}

// Warning records a nonfatal collection failure. Source is a stable,
// machine-readable field name; Message is intended for operators.
type Warning struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

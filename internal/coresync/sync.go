// Package coresync implements one opt-in registration, heartbeat, and desired-
// state retrieval cycle.
// It owns credential state but delegates all HTTPS behavior to the Core client.
package coresync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	coreclient "github.com/henlik/lts-agent/internal/core"
	"github.com/henlik/lts-agent/internal/desired"
	"github.com/henlik/lts-agent/internal/inventory"
)

const (
	RegistrationEndpoint       = "v1/nodes/register"
	DesiredStateEndpointSuffix = "/desired-state"
	DefaultMachineIDPath       = "/etc/machine-id"
	stateSchemaVersion         = 1
	wireSchemaVersion          = 1
)

var (
	errStateNotFound = errors.New("Core state not found")
	nodeIDPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// Client is the authenticated JSON behavior required by the workflow.
type Client interface {
	DoJSONWithBearer(context.Context, string, string, string, any, any) error
}

// Options defines workflow paths and identity without embedding credentials.
type Options struct {
	Enabled             bool
	AgentVersion        string
	MachineIDPath       string
	EnrollmentTokenFile string
	StateFile           string
}

// Dependencies contains filesystem and clock boundaries used in tests.
type Dependencies struct {
	ReadFile   func(string) ([]byte, error)
	Lstat      func(string) (os.FileInfo, error)
	MkdirAll   func(string, os.FileMode) error
	CreateTemp func(string, string) (*os.File, error)
	Rename     func(string, string) error
	Remove     func(string) error
	Now        func() time.Time
}

// Synchronizer performs at most one registration, heartbeat, and desired-state
// retrieval.
type Synchronizer struct {
	client  Client
	options Options
	deps    Dependencies
}

// New constructs a production synchronizer.
func New(client Client, options Options) *Synchronizer {
	return NewWithDependencies(client, options, Dependencies{
		ReadFile:   os.ReadFile,
		Lstat:      os.Lstat,
		MkdirAll:   os.MkdirAll,
		CreateTemp: os.CreateTemp,
		Rename:     os.Rename,
		Remove:     os.Remove,
		Now:        time.Now,
	})
}

// NewWithDependencies constructs a synchronizer with deterministic boundaries.
func NewWithDependencies(client Client, options Options, deps Dependencies) *Synchronizer {
	if options.MachineIDPath == "" {
		options.MachineIDPath = DefaultMachineIDPath
	}
	return &Synchronizer{client: client, options: options, deps: deps}
}

// Sync adds no Core object to the heartbeat snapshot itself. The returned
// summary is attached only after network activity completes, avoiding recursive
// status in the wire document.
func (s *Synchronizer) Sync(ctx context.Context, report inventory.Report) (*inventory.Core, *inventory.DesiredState, []inventory.Warning) {
	unavailableDesired := emptyDesiredState()
	if !s.options.Enabled {
		return &inventory.Core{
			Registration: inventory.Operation{Status: "disabled"},
			Heartbeat:    inventory.Operation{Status: "disabled"},
			DesiredState: inventory.Operation{Status: "disabled"},
		}, unavailableDesired, nil
	}

	result := &inventory.Core{
		Enabled:      true,
		Registration: inventory.Operation{Status: "not_needed"},
		Heartbeat:    inventory.Operation{Status: "skipped"},
		DesiredState: inventory.Operation{Status: "skipped"},
	}
	state, err := s.loadState()
	if errors.Is(err, errStateNotFound) {
		state, err = s.register(ctx, report, result)
		if err != nil {
			return result, unavailableDesired, []inventory.Warning{{Source: "core.registration", Message: sanitizeError(err)}}
		}
	} else if err != nil {
		result.Registration.Status = "failed"
		return result, unavailableDesired, []inventory.Warning{{Source: "core.state", Message: sanitizeError(err)}}
	}

	result.Registered = true
	result.NodeID = state.NodeID
	warnings := make([]inventory.Warning, 0, 2)
	if result.Registration.Status == "succeeded" {
		if err := s.deps.Remove(s.options.EnrollmentTokenFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			warnings = append(warnings, inventory.Warning{
				Source:  "core.enrollment",
				Message: fmt.Sprintf("remove consumed enrollment token: %v", err),
			})
		}
	}

	result.Heartbeat.Attempted = true
	if err := s.heartbeat(ctx, report, state); err != nil {
		result.Heartbeat.Status = "failed"
		warnings = append(warnings, inventory.Warning{Source: "core.heartbeat", Message: sanitizeError(err)})
	} else {
		result.Heartbeat.Status = "succeeded"
	}

	// Caller cancellation stops the remaining workflow. Ordinary heartbeat
	// failures do not prevent observing desired state.
	if ctx.Err() != nil {
		return result, unavailableDesired, warnings
	}
	result.DesiredState.Attempted = true
	document, err := s.desiredState(ctx, state)
	if err != nil {
		result.DesiredState.Status = "failed"
		warnings = append(warnings, inventory.Warning{Source: "core.desired_state", Message: sanitizeError(err)})
		return result, unavailableDesired, warnings
	}
	result.DesiredState.Status = "succeeded"
	desiredInventory := document.Inventory()
	return result, &desiredInventory, warnings
}

type stateDocument struct {
	SchemaVersion int    `json:"schema_version"`
	NodeID        string `json:"node_id"`
	AgentToken    string `json:"agent_token"`
}

type registrationRequest struct {
	SchemaVersion   int              `json:"schema_version"`
	NodeFingerprint string           `json:"node_fingerprint"`
	AgentVersion    string           `json:"agent_version"`
	Inventory       inventory.Report `json:"inventory"`
}

type registrationResponse struct {
	SchemaVersion int    `json:"schema_version"`
	NodeID        string `json:"node_id"`
	AgentToken    string `json:"agent_token"`
}

func (r *registrationResponse) UnmarshalJSON(data []byte) error {
	type document registrationResponse
	var decoded document
	if err := decodeStrict(data, &decoded); err != nil {
		return err
	}
	*r = registrationResponse(decoded)
	return nil
}

type heartbeatRequest struct {
	SchemaVersion int              `json:"schema_version"`
	SentAt        string           `json:"sent_at"`
	AgentVersion  string           `json:"agent_version"`
	Inventory     inventory.Report `json:"inventory"`
}

func (s *Synchronizer) register(ctx context.Context, report inventory.Report, result *inventory.Core) (stateDocument, error) {
	result.Registration.Attempted = true
	result.Registration.Status = "failed"
	fingerprint, err := s.fingerprint()
	if err != nil {
		return stateDocument{}, err
	}
	token, err := s.readSecret(s.options.EnrollmentTokenFile, "enrollment token")
	if err != nil {
		return stateDocument{}, err
	}

	report.Core = nil
	report.DesiredState = nil
	var response registrationResponse
	err = s.client.DoJSONWithBearer(ctx, http.MethodPost, RegistrationEndpoint, token, registrationRequest{
		SchemaVersion:   wireSchemaVersion,
		NodeFingerprint: fingerprint,
		AgentVersion:    s.options.AgentVersion,
		Inventory:       report,
	}, &response)
	if err != nil {
		return stateDocument{}, err
	}
	if response.SchemaVersion != wireSchemaVersion {
		return stateDocument{}, fmt.Errorf("registration response has unsupported schema_version %d", response.SchemaVersion)
	}
	if !nodeIDPattern.MatchString(response.NodeID) {
		return stateDocument{}, fmt.Errorf("registration response contains an invalid node_id")
	}
	if err := coreclient.ValidateBearer(response.AgentToken); err != nil {
		return stateDocument{}, fmt.Errorf("registration response contains an invalid agent_token")
	}

	state := stateDocument{SchemaVersion: stateSchemaVersion, NodeID: response.NodeID, AgentToken: response.AgentToken}
	if err := s.writeState(state); err != nil {
		return stateDocument{}, fmt.Errorf("persist Core state: %w", err)
	}
	result.Registration.Status = "succeeded"
	return state, nil
}

func (s *Synchronizer) heartbeat(ctx context.Context, report inventory.Report, state stateDocument) error {
	report.Core = nil
	report.DesiredState = nil
	endpoint := "v1/nodes/" + url.PathEscape(state.NodeID) + "/heartbeat"
	return s.client.DoJSONWithBearer(ctx, http.MethodPost, endpoint, state.AgentToken, heartbeatRequest{
		SchemaVersion: wireSchemaVersion,
		SentAt:        s.deps.Now().UTC().Format(time.RFC3339Nano),
		AgentVersion:  s.options.AgentVersion,
		Inventory:     report,
	}, nil)
}

func (s *Synchronizer) desiredState(ctx context.Context, state stateDocument) (desired.Document, error) {
	endpoint := "v1/nodes/" + url.PathEscape(state.NodeID) + DesiredStateEndpointSuffix
	var response desired.Document
	if err := s.client.DoJSONWithBearer(ctx, http.MethodGet, endpoint, state.AgentToken, nil, &response); err != nil {
		return desired.Document{}, err
	}
	return response, nil
}

func emptyDesiredState() *inventory.DesiredState {
	return &inventory.DesiredState{Roles: []string{}, Capabilities: []string{}}
}

func (s *Synchronizer) fingerprint() (string, error) {
	data, err := s.deps.ReadFile(s.options.MachineIDPath)
	if err != nil {
		return "", fmt.Errorf("read machine ID: %w", err)
	}
	machineID := strings.TrimSpace(string(data))
	if len(machineID) != 32 {
		return "", fmt.Errorf("machine ID must contain 32 hexadecimal characters")
	}
	if _, err := hex.DecodeString(machineID); err != nil {
		return "", fmt.Errorf("machine ID must contain 32 hexadecimal characters")
	}
	hash := sha256.Sum256([]byte("lts-agent-registration-v1:" + strings.ToLower(machineID)))
	return hex.EncodeToString(hash[:]), nil
}

func (s *Synchronizer) loadState() (stateDocument, error) {
	info, err := s.deps.Lstat(s.options.StateFile)
	if errors.Is(err, os.ErrNotExist) {
		return stateDocument{}, errStateNotFound
	}
	if err != nil {
		return stateDocument{}, fmt.Errorf("inspect Core state: %w", err)
	}
	if err := validateSecretFile(info, "Core state"); err != nil {
		return stateDocument{}, err
	}
	data, err := s.deps.ReadFile(s.options.StateFile)
	if err != nil {
		return stateDocument{}, fmt.Errorf("read Core state: %w", err)
	}
	var state stateDocument
	if err := decodeStrict(data, &state); err != nil {
		return stateDocument{}, fmt.Errorf("parse Core state: %w", err)
	}
	if state.SchemaVersion != stateSchemaVersion {
		return stateDocument{}, fmt.Errorf("Core state has unsupported schema_version %d", state.SchemaVersion)
	}
	if !nodeIDPattern.MatchString(state.NodeID) {
		return stateDocument{}, fmt.Errorf("Core state contains an invalid node_id")
	}
	if err := coreclient.ValidateBearer(state.AgentToken); err != nil {
		return stateDocument{}, fmt.Errorf("Core state contains an invalid agent_token")
	}
	return state, nil
}

func (s *Synchronizer) readSecret(path, name string) (string, error) {
	info, err := s.deps.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", name, err)
	}
	if err := validateSecretFile(info, name); err != nil {
		return "", err
	}
	data, err := s.deps.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	token := strings.TrimSpace(string(data))
	if err := coreclient.ValidateBearer(token); err != nil {
		return "", fmt.Errorf("%s is invalid", name)
	}
	return token, nil
}

func validateSecretFile(info os.FileInfo, name string) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular non-symlink file", name)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s permissions must not grant group or world access", name)
	}
	return nil
}

func (s *Synchronizer) writeState(state stateDocument) (returnErr error) {
	directory := filepath.Dir(s.options.StateFile)
	if err := s.deps.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	info, err := s.deps.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state directory must be a non-symlink directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("state directory permissions must not grant group or world access")
	}

	temporary, err := s.deps.CreateTemp(directory, ".state-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		if returnErr != nil {
			_ = temporary.Close()
			_ = s.deps.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(state); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := s.deps.Rename(temporaryPath, s.options.StateFile); err != nil {
		return err
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return fmt.Errorf("invalid trailing content: %w", err)
	}
	return nil
}

func sanitizeError(err error) string {
	var statusError *coreclient.HTTPError
	if errors.As(err, &statusError) {
		return fmt.Sprintf("LTS Core returned HTTP %d", statusError.StatusCode)
	}
	return err.Error()
}

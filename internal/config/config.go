// Package config loads and validates the local lts-agent configuration file.
// Configuration is strict so administrator typos cannot silently change agent
// behavior.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/henlik/lts-agent/internal/check"
)

const (
	DefaultPath                = "/opt/lts/config/lts-agent.json"
	SupportedSchemaVersion     = 2
	MinimumSchemaVersion       = 1
	MinTimeoutSeconds          = 1
	MaxTimeoutSeconds          = 300
	MinOutputBytes             = 1024
	MaxOutputBytes             = 1024 * 1024
	DefaultCoreTimeoutSeconds  = 10
	MinCoreTimeoutSeconds      = 1
	MaxCoreTimeoutSeconds      = 120
	DefaultEnrollmentTokenPath = "/var/lib/lts-agent/enrollment-token"
	DefaultStatePath           = "/var/lib/lts-agent/state.json"
)

// Config is the effective configuration after defaults and file overrides have
// been merged.
type Config struct {
	SchemaVersion int
	Checks        Checks
	Logging       Logging
	Core          Core
}

// Checks controls execution of the existing local LBI scripts.
type Checks struct {
	ValidationPath string
	HealthPath     string
	TimeoutSeconds int
	MaxOutputBytes int
}

// Logging controls structured operational logs written to stderr.
type Logging struct {
	Level string
}

// Core controls the opt-in registration, heartbeat, and desired-state workflow.
type Core struct {
	Enabled               bool
	BaseURL               string
	CAFile                string
	RequestTimeoutSeconds int
	EnrollmentTokenFile   string
	StateFile             string
}

// ReadFileFunc is the filesystem boundary required by Load.
type ReadFileFunc func(string) ([]byte, error)

// Defaults returns the complete zero-configuration behavior.
func Defaults() Config {
	return Config{
		SchemaVersion: SupportedSchemaVersion,
		Checks: Checks{
			ValidationPath: check.ValidationPath,
			HealthPath:     check.HealthPath,
			TimeoutSeconds: int(check.DefaultTimeout.Seconds()),
			MaxOutputBytes: check.MaxOutputBytes,
		},
		Logging: Logging{Level: "info"},
		Core: Core{
			RequestTimeoutSeconds: DefaultCoreTimeoutSeconds,
			EnrollmentTokenFile:   DefaultEnrollmentTokenPath,
			StateFile:             DefaultStatePath,
		},
	}
}

// Load reads the production configuration path. Loaded is false only when the
// file does not exist and defaults were therefore selected.
func Load() (configuration Config, loaded bool, err error) {
	return LoadWithDependencies(DefaultPath, os.ReadFile)
}

// LoadWithDependencies supports deterministic tests and alternate roots.
func LoadWithDependencies(path string, readFile ReadFileFunc) (Config, bool, error) {
	data, err := readFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Defaults(), false, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("read %s: %w", path, err)
	}

	configuration, err := parse(data)
	if err != nil {
		return Config{}, true, fmt.Errorf("parse %s: %w", path, err)
	}
	return configuration, true, nil
}

type document struct {
	SchemaVersion *int            `json:"schema_version"`
	Checks        json.RawMessage `json:"checks"`
	Logging       json.RawMessage `json:"logging"`
	Core          json.RawMessage `json:"core"`
}

type checksDocument struct {
	ValidationPath *string `json:"validation_path"`
	HealthPath     *string `json:"health_path"`
	TimeoutSeconds *int    `json:"timeout_seconds"`
	MaxOutputBytes *int    `json:"max_output_bytes"`
}

type loggingDocument struct {
	Level *string `json:"level"`
}

type coreDocument struct {
	Enabled               *bool   `json:"enabled"`
	BaseURL               *string `json:"base_url"`
	CAFile                *string `json:"ca_file"`
	RequestTimeoutSeconds *int    `json:"request_timeout_seconds"`
	EnrollmentTokenFile   *string `json:"enrollment_token_file"`
	StateFile             *string `json:"state_file"`
}

func parse(data []byte) (Config, error) {
	var raw document
	if err := decodeStrict(data, &raw); err != nil {
		return Config{}, err
	}
	if raw.SchemaVersion == nil {
		return Config{}, fmt.Errorf("schema_version is required")
	}
	if *raw.SchemaVersion < MinimumSchemaVersion || *raw.SchemaVersion > SupportedSchemaVersion {
		return Config{}, fmt.Errorf("unsupported schema_version %d", *raw.SchemaVersion)
	}

	effective := Defaults()
	effective.SchemaVersion = *raw.SchemaVersion
	if len(raw.Checks) > 0 {
		if isNull(raw.Checks) {
			return Config{}, fmt.Errorf("checks must be an object")
		}
		if err := rejectNullFields("checks", raw.Checks); err != nil {
			return Config{}, err
		}
		var overrides checksDocument
		if err := decodeStrict(raw.Checks, &overrides); err != nil {
			return Config{}, fmt.Errorf("checks: %w", err)
		}
		applyCheckOverrides(&effective.Checks, overrides)
	}
	if len(raw.Logging) > 0 {
		if isNull(raw.Logging) {
			return Config{}, fmt.Errorf("logging must be an object")
		}
		if err := rejectNullFields("logging", raw.Logging); err != nil {
			return Config{}, err
		}
		var overrides loggingDocument
		if err := decodeStrict(raw.Logging, &overrides); err != nil {
			return Config{}, fmt.Errorf("logging: %w", err)
		}
		if overrides.Level != nil {
			effective.Logging.Level = *overrides.Level
		}
	}
	if len(raw.Core) > 0 {
		if *raw.SchemaVersion < 2 {
			return Config{}, fmt.Errorf("core requires schema_version 2")
		}
		if isNull(raw.Core) {
			return Config{}, fmt.Errorf("core must be an object")
		}
		if err := rejectNullFields("core", raw.Core); err != nil {
			return Config{}, err
		}
		var overrides coreDocument
		if err := decodeStrict(raw.Core, &overrides); err != nil {
			return Config{}, fmt.Errorf("core: %w", err)
		}
		applyCoreOverrides(&effective.Core, overrides)
	}

	if err := validate(effective); err != nil {
		return Config{}, err
	}
	return effective, nil
}

func applyCoreOverrides(target *Core, overrides coreDocument) {
	if overrides.Enabled != nil {
		target.Enabled = *overrides.Enabled
	}
	if overrides.BaseURL != nil {
		target.BaseURL = *overrides.BaseURL
	}
	if overrides.CAFile != nil {
		target.CAFile = *overrides.CAFile
	}
	if overrides.RequestTimeoutSeconds != nil {
		target.RequestTimeoutSeconds = *overrides.RequestTimeoutSeconds
	}
	if overrides.EnrollmentTokenFile != nil {
		target.EnrollmentTokenFile = *overrides.EnrollmentTokenFile
	}
	if overrides.StateFile != nil {
		target.StateFile = *overrides.StateFile
	}
}

func applyCheckOverrides(target *Checks, overrides checksDocument) {
	if overrides.ValidationPath != nil {
		target.ValidationPath = *overrides.ValidationPath
	}
	if overrides.HealthPath != nil {
		target.HealthPath = *overrides.HealthPath
	}
	if overrides.TimeoutSeconds != nil {
		target.TimeoutSeconds = *overrides.TimeoutSeconds
	}
	if overrides.MaxOutputBytes != nil {
		target.MaxOutputBytes = *overrides.MaxOutputBytes
	}
}

func validate(configuration Config) error {
	if !filepath.IsAbs(configuration.Checks.ValidationPath) {
		return fmt.Errorf("checks.validation_path must be a non-empty absolute path")
	}
	if !filepath.IsAbs(configuration.Checks.HealthPath) {
		return fmt.Errorf("checks.health_path must be a non-empty absolute path")
	}
	if configuration.Checks.TimeoutSeconds < MinTimeoutSeconds || configuration.Checks.TimeoutSeconds > MaxTimeoutSeconds {
		return fmt.Errorf("checks.timeout_seconds must be between %d and %d", MinTimeoutSeconds, MaxTimeoutSeconds)
	}
	if configuration.Checks.MaxOutputBytes < MinOutputBytes || configuration.Checks.MaxOutputBytes > MaxOutputBytes {
		return fmt.Errorf("checks.max_output_bytes must be between %d and %d", MinOutputBytes, MaxOutputBytes)
	}
	if _, err := LogLevel(configuration.Logging.Level); err != nil {
		return err
	}
	if configuration.Core.Enabled && configuration.Core.BaseURL == "" {
		return fmt.Errorf("core.base_url is required when core is enabled")
	}
	if configuration.Core.BaseURL != "" {
		parsed, err := url.Parse(configuration.Core.BaseURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("core.base_url must be an HTTPS origin without credentials, query, or fragment")
		}
	}
	if configuration.Core.CAFile != "" && !filepath.IsAbs(configuration.Core.CAFile) {
		return fmt.Errorf("core.ca_file must be an absolute path when set")
	}
	if !filepath.IsAbs(configuration.Core.EnrollmentTokenFile) {
		return fmt.Errorf("core.enrollment_token_file must be an absolute path")
	}
	if !filepath.IsAbs(configuration.Core.StateFile) {
		return fmt.Errorf("core.state_file must be an absolute path")
	}
	if configuration.Core.RequestTimeoutSeconds < MinCoreTimeoutSeconds || configuration.Core.RequestTimeoutSeconds > MaxCoreTimeoutSeconds {
		return fmt.Errorf("core.request_timeout_seconds must be between %d and %d", MinCoreTimeoutSeconds, MaxCoreTimeoutSeconds)
	}
	return nil
}

// LogLevel converts the validated configuration value to slog's level type.
func LogLevel(level string) (slog.Level, error) {
	switch level {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("logging.level must be one of debug, info, warn, or error")
	}
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

func isNull(value json.RawMessage) bool {
	return strings.TrimSpace(string(value)) == "null"
}

func rejectNullFields(section string, data json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		// The strict typed decoder that follows provides the clearer shape error.
		return nil
	}
	for name, value := range fields {
		if isNull(value) {
			return fmt.Errorf("%s.%s must not be null", section, name)
		}
	}
	return nil
}

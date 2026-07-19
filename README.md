# LTS Agent

LTS Agent is the node-side software for the Likone Technology Stack (LTS). It
will eventually register nodes, report health, apply capabilities, and execute
jobs from LTS Core. Version 0.7.0 packages the single-run agent for hardened,
scheduled operation on Ubuntu-based LBI nodes.

The command runs without root privileges and prints one stable, human-readable
JSON document to standard output. Missing inventory sources do not prevent the
remaining data from being returned.

## Current scope

Version 0.7.0 collects:

- Agent version
- Node hostname
- LBI metadata from `/etc/lbi-release`, when present
- Operating-system name from `/etc/os-release`
- Kernel release from `/proc/sys/kernel/osrelease`, with `uname -r` as a fallback
- Normalized architecture such as `x86_64` or `aarch64`
- Timezone from `/etc/timezone`, `/etc/localtime`, or the Go runtime
- Assigned roles and capabilities from `/opt/lts/roles/assigned.json`
- LBI compliance validation from `/opt/lts/scripts/lbi-validate.sh`
- Runtime health from `/opt/lts/scripts/health-check.sh`

It also supports strict local configuration, structured operational logging,
secure synchronization with LTS Core, and external scheduling through a systemd
timer.

It does not install software, change host configuration, require privileged
access, apply roles or capabilities, or implement jobs, updates, retries, or an
internal daemon loop.

## Architecture

The command under `cmd/lts-agent` creates and connects focused internal
components:

- `internal/agent` coordinates collection and JSON encoding.
- `internal/app` owns configuration, logging, collection, and output lifecycle.
- `internal/check` executes and normalizes the existing LBI validation and
  health scripts.
- `internal/config` loads, defaults, and validates the local configuration.
- `internal/core` provides bounded, TLS-verified and bearer-authenticated JSON
  transport.
- `internal/coresync` owns registration, credential state, and heartbeat.
- `internal/inventory` owns the stable JSON schema.
- `internal/lbi` safely parses LBI metadata without evaluating shell content.
- `internal/role` strictly validates and inventories assigned roles and
  capabilities.
- `internal/system` collects host and operating-system values through injectable
  filesystem and command boundaries.

Tests live beside the packages they cover, following standard Go conventions.
The narrow collector interfaces allow future configuration, logging, and
networking modules to be introduced without coupling them to the local platform
implementation.

## Requirements

- Go 1.25 or later
- Linux for the intended deployment target

Development and tests also work on macOS. The Linux-specific data sources are
accessed through fallbacks and injectable dependencies, so an LBI VM is not
required to build or test the project.

## Build

Build a binary for the current development host:

```sh
make build
./bin/lts-agent
```

Build the Linux/amd64 binary intended for the current x86-64 LBI:

```sh
make build-linux-amd64
file bin/lts-agent-linux-amd64
```

The project uses only the Go standard library. No dependency download or network
access is required.

Build the Debian/amd64 deployment package on Ubuntu or Debian, where
`dpkg-deb` is available:

```sh
make package-deb
make package-verify
```

`make package-stage` prepares and validates the package filesystem on macOS
without requiring Debian tooling. The complete Linux release check is
`make release-linux-amd64`.

## Test and quality checks

```sh
make fmt
make vet
make test
make test-race
```

To remove generated binaries and the repository-local Go build cache:

```sh
make clean
```

## JSON output

On an LBI node, output has this shape:

```json
{
  "agent": {
    "version": "0.7.0"
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
  },
  "core": {
    "enabled": true,
    "registered": true,
    "node_id": "node-123",
    "registration": {
      "attempted": false,
      "status": "not_needed"
    },
    "heartbeat": {
      "attempted": true,
      "status": "succeeded"
    }
  }
}
```

`lbi.available` is always present. Optional LBI metadata fields are omitted when
the release file is absent or contains only partial metadata. Collection failures
are nonfatal and appear in an optional top-level `warnings` array:

```json
{
  "source": "lbi",
  "message": "read /etc/lbi-release: file does not exist"
}
```

Field-level collection failures leave the corresponding string empty. The
process exits successfully after emitting valid JSON; it exits nonzero only if
configuration is invalid or the JSON document cannot be encoded or written.

## Configuration

The agent reads `/opt/lts/config/lts-agent.json`. The file is optional: when it
does not exist, the agent uses the following defaults and records a
`config_defaults_used` log event. An existing file that cannot be read or
validated is fatal and prevents inventory output.

```json
{
  "schema_version": 2,
  "checks": {
    "validation_path": "/opt/lts/scripts/lbi-validate.sh",
    "health_path": "/opt/lts/scripts/health-check.sh",
    "timeout_seconds": 30,
    "max_output_bytes": 65536
  },
  "logging": {
    "level": "info"
  },
  "core": {
    "enabled": false,
    "base_url": "https://core.example/api/",
    "ca_file": "",
    "request_timeout_seconds": 10,
    "enrollment_token_file": "/var/lib/lts-agent/enrollment-token",
    "state_file": "/var/lib/lts-agent/state.json"
  }
}
```

`schema_version` is required. Version 1 remains supported and always disables
Core. Version 2 adds the optional `core` section. The `checks`, `logging`, and
known fields are optional overrides. Unknown fields, malformed or trailing JSON,
null sections, unsupported schemas, relative file paths, invalid URLs or log
levels, and out-of-range limits are rejected. Core request timeouts must be
1–120 seconds.

Allowed log levels are `debug`, `info`, `warn`, and `error`. See
[`configs/lts-agent.example.json`](configs/lts-agent.example.json) for the full
example.

## Structured logging

Inventory remains one indented JSON document on stdout. Operational logs are
newline-delimited JSON records on stderr, so piping inventory to tools such as
`jq` remains safe. Every log contains `time`, `level`, `msg`, `event`, and
`agent_version`.

Lifecycle events are `config_defaults_used` or `config_loaded`,
`collection_started`, one `inventory_warning` per report warning, and
`collection_completed`. Fatal startup and output failures use `config_invalid`
and `inventory_write_failed`. The configured minimum log level applies to all
events. Enabled Core workflows additionally emit `core_sync_started` and
`core_sync_completed` without credential values.

## Role and capability assignments

The agent reads `/opt/lts/roles/assigned.json`. A version 1 assignment document
has this strict shape:

```json
{
  "schema_version": 1,
  "roles": ["application-node"],
  "capabilities": ["docker", "postgresql"]
}
```

All three fields are required. Unknown fields, unsupported schema versions,
incorrect types, malformed JSON, and trailing content make the assignment
unavailable and produce a warning. The agent never creates or modifies this
file.

Identifiers may contain lowercase ASCII letters, digits, and internal hyphens.
Valid identifiers are deduplicated and sorted. An invalid individual identifier
is omitted with a warning while other valid assignments remain available. See
[`configs/assigned.example.json`](configs/assigned.example.json) for a complete
example.

## Validation and health checks

The agent executes the existing LBI scripts directly, in validation-then-health
order, without a shell or `sudo`. Each script receives an independent 30-second
deadline. Combined standard output and standard error are retained up to 64 KiB;
larger output is truncated and marked with `truncated: true`.

Validation uses `passed` for exit code 0 and `failed` for any other normal exit.
Health uses the LBI contract `0 = healthy`, `1 = degraded`, and `2 = critical`.
Other health exit codes become `error`. Missing or non-executable scripts are
`unavailable`, and checks that exceed their deadline are `timeout`.

Failed validation and degraded or critical health are valid check results, not
agent execution failures. The command still exits zero after producing complete
JSON. Collection warnings are added only when a script is unavailable, times
out, is cancelled, terminates abnormally, or returns an unsupported health exit
code.

## LTS Core HTTPS client

Version 0.7 uses the context-aware JSON client for registration and heartbeat
only when schema-v2 configuration sets `core.enabled` to true. Disabled and
schema-v1 configurations perform no Core network activity.

The client requires an HTTPS origin, verifies hostnames with the system trust
store, optionally appends a private PEM CA bundle, and enforces TLS 1.2 or newer.
It rejects origin overrides and path traversal, does not follow redirects, and
limits every response to 1 MiB. Requests set JSON headers and a caller-provided
User-Agent. Non-2xx responses return a typed status error with a bounded body
excerpt.

Registration exchanges a one-time enrollment bearer token for a node ID and a
node-specific bearer token. Each invocation then sends one inventory heartbeat.
There are no automatic retries or background loop; scheduling remains external.
The client does not support insecure TLS or place credentials in logs.

### Provision enrollment state

The Debian package creates the locked `lts-agent` service account and its state
directory. Install a one-time enrollment token received through a secure
out-of-band channel for that account:

```sh
sudo install -d -m 0700 -o lts-agent -g lts-agent /var/lib/lts-agent
sudo install -m 0600 -o lts-agent -g lts-agent enrollment-token /var/lib/lts-agent/enrollment-token
```

Enable Core in `/opt/lts/config/lts-agent.json` and run `lts-agent`. Successful
registration atomically writes `/var/lib/lts-agent/state.json` with mode `0600`,
deletes the consumed enrollment token, and immediately sends one heartbeat.
Subsequent invocations load state and send only the heartbeat.

Invalid state is never overwritten automatically. For HTTP authentication,
missing-node, or server failures, inspect structured logs and Core state before
provisioning a new enrollment token. See
[`docs/LTS-CORE-API-v1.md`](docs/LTS-CORE-API-v1.md) for the wire contract.

## Scheduled deployment

The Debian package installs `/usr/bin/lts-agent`, a hardened oneshot service,
and a persistent timer. The timer starts two minutes after boot and runs every
five minutes with up to 30 seconds of random delay. systemd will not start a
second copy while the same service unit is active.

Inventory remains on stdout and operational logs remain on stderr. systemd
captures both streams as separate journal records for scheduled executions:

```sh
systemctl status lts-agent.timer
systemctl list-timers lts-agent.timer
journalctl -u lts-agent.service
```

Package removal deliberately preserves `/var/lib/lts-agent` and the service
account so reinstalling cannot silently discard the node identity. See
[`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) for package installation, upgrades,
manual cleanup, recovery, and complete LBI verification.

LBI 1.0 Build 001 scripts require a compatibility adjustment before they can
report SSH and UFW state from the unprivileged service account. See
[`docs/LBI-COMPATIBILITY.md`](docs/LBI-COMPATIBILITY.md); the package does not
alter LBI-owned scripts automatically.

## Verify on an LBI clone

Build the package on Ubuntu or Debian and copy it to a disposable LBI clone,
replacing `NODE` with its address:

```sh
make package-verify
scp bin/lts-agent_0.7.0_amd64.deb ltsadmin@NODE:/tmp/
ssh ltsadmin@NODE 'sudo apt install /tmp/lts-agent_0.7.0_amd64.deb'
ssh ltsadmin@NODE 'sudo -u lts-agent /usr/bin/lts-agent | jq .'
```

To install explicit configuration, review the example first and then place it
under the root-owned LBI configuration directory:

```sh
scp configs/lts-agent.example.json ltsadmin@NODE:/tmp/lts-agent.json
ssh ltsadmin@NODE 'sudo install -m 0644 -o root -g root /tmp/lts-agent.json /opt/lts/config/lts-agent.json'
```

Confirm that both `checks.validation` and `checks.health` are available, that
their statuses agree with direct script execution, and that the agent itself did
not receive root privileges. Confirm the timer is active and inspect the journal
after its next activation.

## Roadmap

- **v0.8:** Retrieve and report desired state without applying changes.
- **v0.9:** Add controlled capability and role application.
- **v1.0:** Deliver the production-ready lifecycle, security, and update model.

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE).

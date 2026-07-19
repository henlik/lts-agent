You are helping build the Likone Technology Stack (LTS), a platform for managing distributed infrastructure, virtual machines, services, and applications across multiple sites.

We have completed the first infrastructure milestone: LBI, the LTS Base Image.

PROJECT PURPOSE
===============

LTS is intended to become a multi-site infrastructure platform with:

- Proxmox-based virtualization
- Standardized Linux base images
- Central orchestration
- Node registration and health reporting
- Capability/role assignment
- Automated application deployment
- Monitoring and backups
- Support for applications such as UmeCare, OpenMRS, ERP, PBX, gateways, databases, and other workloads

The long-term architecture is:

LTS Core
    |
    +-- LTS Agent on each node
    |
    +-- LTS CLI
    |
    +-- Capability / role engine
    |
    +-- Applications

The design principle is separation of concerns:

1. LBI
   Standard operating-system baseline

2. LTS Agent
   Node inventory, validation, health, registration, heartbeat, and later job execution

3. Capabilities / Roles
   Docker, Application Node, PostgreSQL, Backup, Monitoring, Gateway, etc.

4. Applications
   umacare, lts, ldk, PBX, AI services, and other workloads


CURRENT INFRASTRUCTURE
======================

A Proxmox VE server is running at:

- Proxmox IP: 172.16.0.11
- Proxmox VE 9.x
- Debian 13-based host
- Main VM storage: local-lvm
- Network gateway: 172.16.0.1

A WireGuard-based remote-access architecture is already operational through an LTS Core VPS.

The transport/network layer is considered complete for now.


LBI STATUS
==========

We created LBI 1.0 Build 001 based on:

- Ubuntu Server 24.04.4 LTS
- Kernel 6.8 series
- x86-64
- KVM / Proxmox
- Q35 machine type
- UEFI
- VirtIO SCSI
- VirtIO NIC
- QEMU Guest Agent
- 2+ vCPU
- 4+ GB RAM
- 32+ GB disk
- LVM-based Ubuntu installation
- Timezone: Africa/Lubumbashi

Administrative user:

- Username: ltsadmin
- Has sudo access
- Root SSH login disabled

Template hostname during build:

- lbi

LBI metadata exists at:

/etc/lbi-release

Current contents:

LBI_NAME="LTS Base Image"
LBI_SHORT="LBI"
LBI_VERSION="1.0"
LBI_BUILD="001"
BASE_OS="Ubuntu 24.04.4 LTS"
MAINTAINER="Likone Technologies"


LBI DIRECTORY STANDARD
======================

The following directories exist:

/opt/lts/bootstrap
/opt/lts/config
/opt/lts/monitoring
/opt/lts/roles
/opt/lts/scripts
/opt/lts/spec
/opt/lts/releases

/opt/apps
/opt/backups
/opt/data
/opt/logs

Platform-controlled files under /opt/lts are root-owned.

Application and data directories may later be owned by service-specific users.


INSTALLED BASE SERVICES
=======================

The LBI includes and enables:

- qemu-guest-agent
- chrony
- fail2ban
- ssh
- ufw
- unattended-upgrades

Base utilities include:

- curl
- wget
- git
- vim
- nano
- htop
- iftop
- iotop
- jq
- tree
- rsync
- zip
- unzip
- bash-completion
- net-tools
- iputils-ping
- dnsutils
- tcpdump
- traceroute
- ca-certificates
- gnupg
- software-properties-common
- logrotate


SECURITY BASELINE
=================

UFW:

- Default incoming: deny
- Default outgoing: allow
- OpenSSH allowed

SSH baseline:

PermitRootLogin no
PubkeyAuthentication yes
PasswordAuthentication yes
KbdInteractiveAuthentication no
PermitEmptyPasswords no
X11Forwarding no
AllowAgentForwarding yes
AllowTcpForwarding yes
MaxAuthTries 3
ClientAliveInterval 300
ClientAliveCountMax 2

Password authentication remains enabled during the lab/build phase. It may later be disabled after SSH keys are deployed and verified.


LBI SPECIFICATION
=================

A formal specification exists at:

/opt/lts/spec/LBI-SPECIFICATION-v1.0.md

It defines:

- Required users
- Directory contracts
- Packages
- Required services
- Security baseline
- Metadata
- Bootstrap interface
- Logging rules
- Backup conventions
- Secret handling
- Role/capability contract
- Validation
- Health checks
- Template preparation
- Versioning
- Compatibility promises

A checksum file also exists for the specification.


BOOTSTRAP
=========

The bootstrap entry point is:

/opt/lts/bootstrap/init.sh

It currently:

- Reads /etc/lbi-release
- Prints LBI name, version, and build
- Prints hostname and kernel
- Uses set -euo pipefail

It is intentionally minimal.

Long term, bootstrap will:

- Register a node
- Configure the LTS Agent
- Apply capabilities/roles
- Configure monitoring
- Configure backups
- Prepare workloads


VALIDATION
==========

A compliance validator exists at:

/opt/lts/scripts/lbi-validate.sh

It currently checks:

- LBI metadata
- Bootstrap script
- LBI specification
- LTS CLI
- qemu-guest-agent
- chrony
- fail2ban
- SSH configuration
- UFW
- Hostname
- Required directories

The validator currently passes all checks and exits 0.


HEALTH CHECK
============

A runtime health check exists at:

/opt/lts/scripts/health-check.sh

It checks:

- LBI metadata
- Required services
- NTP synchronization
- Failed systemd services
- Root filesystem usage
- Available memory
- Default route
- DNS resolution
- UFW status

Exit-code contract:

- 0 = healthy
- 1 = degraded
- 2 = critical

The current LBI health check returns:

- 0 warnings
- 0 criticals
- Exit code 0


LTS CLI
=======

A Bash prototype CLI exists at:

/usr/local/bin/lts

Supported commands:

- lts info
- lts version
- lts validate
- lts health
- lts bootstrap
- lts role
- lts agent
- lts release
- lts help

Example current output:

LTS Base Image 1.0 Build 001

Validation: PASS
Health: Healthy
Role: None
Agent: Not installed

The Bash CLI is only a prototype. A future production CLI may be rewritten in Go.


PROXMOX RELEASE WORKFLOW
========================

VM 100:

- LBI development/build VM
- Keeps snapshots
- Remains editable
- Used for future LBI development

Template 900:

- Full clone of VM 100
- Sanitized
- No snapshots
- Converted successfully into a Proxmox template
- Verified with:

template: 1

Template 900 is the official:

LBI 1.0 Build 001 Template

Before conversion, the release candidate was sanitized by:

- Cleaning APT cache
- Cleaning temporary files
- Cleaning cloud-init state
- Cleaning logs
- Removing SSH host keys
- Clearing /etc/machine-id
- Clearing shell history
- Shutting down immediately
- Converting to a template without rebooting

The first future clone should generate:

- A unique machine ID
- Unique SSH host keys
- Its own DHCP lease
- A unique hostname


WHAT WE ARE BUILDING NOW
========================

We are starting the first real software component:

lts-agent

Development must now happen outside the VM, on macOS or through Codex.

The VM and template are release targets, not development environments.

Expected workflow:

Mac / Codex
    -> Git repository
    -> Tests
    -> Build
    -> Release binary
    -> Install into an LBI clone
    -> Verify
    -> Later include in a new LBI release


LTS-AGENT GOAL
==============

The LTS Agent runs on every LTS-managed node.

Long-term responsibilities:

- Read LBI metadata
- Collect system inventory
- Run validation
- Run health checks
- Read assigned capabilities/roles
- Register with LTS Core
- Authenticate securely
- Send heartbeats
- Report health and inventory
- Retrieve desired configuration
- Apply capabilities/roles
- Run jobs
- Manage updates
- Verify signatures
- Manage certificates
- Produce structured logs

Do not implement all of this immediately.


LTS-AGENT VERSION PLAN
======================

v0.1:
- Local system inventory
- JSON output

v0.2:
- LBI metadata
- Role/capability information

v0.3:
- Validator integration
- Health-check integration

v0.4:
- Configuration file
- Structured logging

v0.5:
- HTTPS client for LTS Core

v0.6:
- Registration and heartbeat

v1.0:
- Production-ready agent


IMMEDIATE TASK
==============

Build lts-agent v0.1 in Go.

The first version must:

1. Read system inventory
2. Read /etc/lbi-release when present
3. Read the hostname
4. Read kernel version
5. Read OS information
6. Read architecture
7. Read timezone
8. Output clean JSON
9. Work on Linux
10. Be extensively commented
11. Include unit tests
12. Avoid unnecessary external dependencies
13. Fail gracefully when files or commands are unavailable
14. Never require root privileges for inventory collection
15. Be structured so later health, validation, networking, and capability modules can be added cleanly


PROPOSED REPOSITORY
===================

Repository name:

lts-agent

Suggested Go module:

github.com/likonetech/lts-agent

Suggested structure:

lts-agent/
├── cmd/
│   └── lts-agent/
│       └── main.go
├── internal/
│   ├── agent/
│   ├── config/
│   ├── inventory/
│   ├── lbi/
│   ├── platform/
│   ├── role/
│   └── system/
├── configs/
├── docs/
├── scripts/
├── tests/
├── .gitignore
├── Makefile
├── README.md
├── LICENSE
└── go.mod


DESIGN PRINCIPLES
=================

- Prefer simple, explicit Go code
- Avoid premature abstraction
- Define interfaces only where they help testing or replacement
- Use Go standard library where practical
- Keep internal packages focused
- Return errors rather than silently hiding them
- JSON output must be stable and documented
- Do not execute arbitrary shell strings
- Use context.Context where operations may later block
- Use dependency injection for testability
- Keep Linux-specific code isolated
- Add comments explaining why, not just what
- Do not implement networking yet
- Do not implement the LTS Core yet
- Do not install Docker
- Do not modify the LBI template
- Do not add a systemd service yet unless separately requested


EXPECTED INITIAL JSON
=====================

The first output may look like:

{
  "agent": {
    "version": "0.1.0"
  },
  "node": {
    "hostname": "lts-app-001"
  },
  "lbi": {
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
  }
}

When LBI metadata is absent, the agent should still return system inventory and mark LBI as unavailable rather than crashing.


FIRST CODEX DELIVERABLES
========================

Please do the following:

1. Inspect the current directory before modifying anything.
2. Create the Go repository structure.
3. Initialize the Go module.
4. Implement lts-agent v0.1.
5. Add unit tests.
6. Add a Makefile with at least:
   - make build
   - make test
   - make fmt
   - make vet
   - make clean
7. Add a README explaining:
   - Purpose
   - Architecture
   - Current scope
   - Build instructions
   - Test instructions
   - Example output
   - Roadmap
8. Add a sensible .gitignore.
9. Use semantic version 0.1.0.
10. Run:
    - gofmt
    - go vet ./...
    - go test ./...
    - go build ./...
11. Fix all failures.
12. Summarize:
    - Files created
    - Architectural choices
    - Test results
    - How to run the binary
    - What remains for v0.2

Do not skip tests.
Do not use placeholder implementations.
Do not invent access to LTS Core.
Do not make network calls.
Do not modify Proxmox or the LBI VM.
Work autonomously, but pause before making any major architectural change that conflicts with the stated repository layout, version plan, or LBI contract.
# Changelog

All notable changes to LTS Agent are recorded here. The project follows semantic
versioning while its public inventory and Core contracts evolve toward v1.0.

## 0.7.1

- Guaranteed a first timer run two minutes after timer activation, including
  package reinstall on an already-running node.
- Preserved the existing two-minute boot delay, five-minute cadence,
  randomized delay, persistent scheduling, and all v0.7.0 data contracts.

## 0.7.0

- Added a hardened systemd oneshot service and five-minute timer.
- Added Debian/amd64 package staging, build, verification, and release targets.
- Added a dedicated unprivileged service account and persistent state handling.
- Added tested LBI compatibility scripts for unprivileged validation and health.
- Allowed local netlink route inspection within the hardened service sandbox.

## 0.6.0

- Added opt-in node registration and one heartbeat per invocation.
- Added schema-v2 Core configuration and secure bearer authentication.
- Added atomic credential-state persistence and nonfatal Core summaries.

This entry records the fully tested v0.6.0 boundary that preceded deployment
packaging. Git commits and release tags remain administrator-controlled.

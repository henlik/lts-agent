# LTS Agent deployment

LTS Agent v0.7 is delivered as an amd64 Debian package for Ubuntu 24.04 LBI
nodes. The package installs a single-run binary and delegates all scheduling and
overlap prevention to systemd.

## Build and inspect the package

Run the full release target on Ubuntu or Debian with Go 1.25 or later and
`dpkg-deb` installed:

```sh
make release-linux-amd64
dpkg-deb --info bin/lts-agent_0.7.0_amd64.deb
dpkg-deb --contents bin/lts-agent_0.7.0_amd64.deb
```

macOS can run `make package-stage` to validate the filesystem layout without
building the final archive. Generated package files live under `build/` and
`bin/` and are removed by `make clean`.

## Install on an LBI node

Copy the package to the node and install it with apt so dependencies are
validated:

```sh
scp bin/lts-agent_0.7.0_amd64.deb ltsadmin@NODE:/tmp/
ssh ltsadmin@NODE 'sudo apt install /tmp/lts-agent_0.7.0_amd64.deb'
```

Installation creates a locked `lts-agent` system account, prepares
`/var/lib/lts-agent` as `lts-agent:lts-agent` with mode `0700`, and enables the
timer. The package does not install an active configuration; an absent
`/opt/lts/config/lts-agent.json` retains safe defaults with Core disabled.

Install reviewed configuration as a root-owned file when overrides are needed:

```sh
sudo install -d -m 0755 -o root -g root /opt/lts/config
sudo install -m 0644 -o root -g root lts-agent.json /opt/lts/config/lts-agent.json
```

The LBI validation and health scripts and assignment file must be readable by
the service account, and both scripts must be executable by it.
The hardened unit permits `AF_NETLINK` because the LBI health script uses
netlink through `ip route` to verify the default route; it grants no additional
process capability.

## Provision enrollment

Core remains disabled until schema-v2 configuration explicitly enables it. To
enroll a node, securely copy its one-time token and install it with secret-only
permissions:

```sh
sudo install -m 0600 -o lts-agent -g lts-agent enrollment-token /var/lib/lts-agent/enrollment-token
sudo systemctl start lts-agent.service
```

Successful registration writes `state.json` with mode `0600`, removes the
enrollment token, and sends the first heartbeat. Never place either token in
command-line arguments, logs, tickets, or source control.

## Operate and diagnose

The timer runs two minutes after boot and then every five minutes, randomized by
up to 30 seconds. A service invocation may run for at most 15 minutes; systemd
does not overlap activations of the same oneshot unit.

```sh
systemctl status lts-agent.timer
systemctl list-timers lts-agent.timer
sudo systemctl start lts-agent.service
journalctl -u lts-agent.service --since today
```

The application still writes one inventory document to stdout and JSON
operational records to stderr. journald captures both streams without changing
the application contract. Use the `event` field to identify operational logs.

For a direct least-privilege check:

```sh
sudo -u lts-agent /usr/bin/lts-agent | jq .
```

Verify that validation and health are available, Core is disabled or registered
as intended, the process has no root privileges, and one invocation sends at
most one registration request and one heartbeat.

## Upgrade, remove, and recover

Upgrade in place with the newer package:

```sh
sudo apt install ./lts-agent_NEW_VERSION_amd64.deb
```

The timer is reloaded and enabled. The service account, state, and enrollment
token remain in place, so an upgrade does not trigger unplanned re-enrollment.

Removing or purging the package stops and disables the timer but deliberately
preserves `/var/lib/lts-agent` and the `lts-agent` account. Reinstalling the
package therefore recovers the same node identity.

Invalid state is never overwritten automatically. Before destructive recovery,
stop the timer, preserve the journal and state for diagnosis, revoke the node
credential in LTS Core, and obtain a replacement enrollment token. Only then may
an administrator explicitly remove `state.json`, install the new token with mode
`0600`, and restart the service.

If the node is permanently decommissioned and its Core credential has been
revoked, its preserved local files and service account may be removed manually:

```sh
sudo systemctl disable --now lts-agent.timer
sudo rm -rf /var/lib/lts-agent
sudo deluser --system lts-agent
sudo delgroup --system lts-agent
```

These cleanup commands are intentionally not run by package maintainer scripts.

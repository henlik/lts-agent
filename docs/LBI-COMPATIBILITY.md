# LBI script compatibility for unprivileged agents

LTS Agent runs validation and health checks directly as the unprivileged
`lts-agent` account. LBI 1.0 Build 001 originally used `sshd -t` and `ufw
status`; both commands require root on Ubuntu 24.04 and therefore produced false
validation and health failures under the agent service account.

The compatibility scripts under `compat/lbi` preserve the established output
and exit-code contracts while using state that an unprivileged process can
reliably observe:

- SSH must be active, and the root-owned, non-writable `99-lts.conf` baseline
  must contain every required LBI directive exactly once.
- UFW must be installed, enabled and active with a successful systemd result;
  its configuration must enable UFW with deny-incoming and allow-outgoing
  defaults.

These runtime checks deliberately do not claim to parse root-only SSH includes
or inspect the live nftables ruleset. The LBI build and release procedure must
retain authoritative privileged checks:

```sh
sudo sshd -t
sudo ufw status verbose
```

## Review and apply

`lbi-unprivileged-checks.patch` is based on the scripts shipped by LBI 1.0 Build
001. Review it before applying it to the LBI source or a disposable clone. The
package also carries complete corrected examples under
`/usr/share/doc/lts-agent/examples/lbi/`.

Back up the three production targets before installation. Install the helper
first, then the corrected scripts, all root-owned:

```sh
sudo install -d -m 0755 -o root -g root /opt/lts/scripts/lib
sudo install -m 0755 -o root -g root lbi-unprivileged-checks.sh /opt/lts/scripts/lib/lbi-unprivileged-checks.sh
sudo install -m 0755 -o root -g root lbi-validate.sh /opt/lts/scripts/lbi-validate.sh
sudo install -m 0755 -o root -g root health-check.sh /opt/lts/scripts/health-check.sh
```

The Debian package never performs these replacements automatically. Promotion
into future LBI builds remains an explicit LBI release decision.

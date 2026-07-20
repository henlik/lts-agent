# LTS Agent v0.7.0 on LBI 1.0 Build 002

The published LTS Agent v0.7.0 package was accepted on 2026-07-20 using a
disposable clone of Proxmox template 901 (`lbi-1-0-build002-template`). The LBI
image was built from private `henlik/lts-base-image` source commit `6f6c27c`.

- Agent tag/source commit: `d8fe43f7054a2c9feb680357fdafee90f2ae83c5`
- Package SHA-256:
  `008a579c17b8559da64dffcf7d48cc998a57d794add28ff7e1513e4e023a1f1b`
- Release: <https://github.com/henlik/lts-agent/releases/tag/v0.7.0>
- LBI metadata: Ubuntu 24.04.4 LTS, LBI version 1.0, build 002

The clone regenerated its machine ID and SSH host keys, received DHCP and
hostname configuration through cloud-init, and passed QEMU Guest Agent,
`sshd -t`, UFW, validation 14/14, and health 0/0 gates. The SSH policy was
`root:root` mode `0644`, allowing the dedicated unprivileged agent to validate
its non-secret directives without granting write access.

The exact release package installed successfully and created the locked
`lts-agent` UID/GID and mode-`0700` state directory. Direct execution proved
that stdout contained one valid inventory JSON document while stderr contained
only valid structured lifecycle records. The service exited 0 with validation
`passed`, health `healthy`, Core disabled, and no permission warning. The
missing optional assignment file produced the sole expected warning.

Live hardening scored `4.0 OK` under `systemd-analyze security`. Two timer runs
started at 12:23:58 and 12:29:30 CAT, completed in 224 ms and 160 ms, and did
not overlap. Reinstall, remove, purge, and reinstall preserved the service
identity and mode-`0600` state/enrollment fixtures byte-for-byte.

One recovery caveat was observed: after purge/reinstall on an already-running
node, the preserved persistent-timer stamp can leave the timer `active
(elapsed)`. Run `systemctl start lts-agent.service` once and confirm
`systemctl list-timers lts-agent.timer`; this re-armed the verified five-minute
cadence. Automating this edge-case recovery is scheduled for the next packaging
revision.

VM 102 and snapshot `lbi-1-0-build002-final` retain the pre-sanitation recovery
point. Template 901 is the release deployment source. VM 100 and template 900
were not modified, and disposable acceptance VM 903 was removed after evidence
collection.

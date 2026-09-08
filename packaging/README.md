# packaging

Privilege model artifacts for ADR-0005: not a full installer yet (that's
`nfpm`/`.deb`/`.rpm` packaging, a later task), just the pieces that
implement the model itself.

## Install the systemd agent

The agent always runs as the unprivileged `bitacora` user. Run these commands
as root after placing the Linux journald artifact at
`/usr/bin/bitacora-agent`.

### Build and retrieve the Linux journald artifact

CI is the supported build location for Linux hosts that collect journald. The
`Build Linux amd64 agent with journald` job compiles
`bitacora-agent-linux-amd64-journald` with `CGO_ENABLED=1` and
`libsystemd-dev`, then publishes it as the
`bitacora-agent-linux-amd64-journald` workflow artifact. Download that
artifact from the successful workflow run that contains the commit being
deployed; do not build on each monitored host.

Before replacing the installed binary, verify that the fallback reader was not
compiled into it. `ldd` is not a valid check because sdjournal loads
libsystemd with `dlopen`:

```sh
if strings bitacora-agent-linux-amd64-journald | grep -Fq 'requires cgo and libsystemd'; then
  echo 'invalid journald artifact' >&2
  exit 1
fi
install -o root -g root -m 0755 bitacora-agent-linux-amd64-journald /usr/bin/bitacora-agent
```

For a pre-existing installation, migrate the token directory and token before
starting the service:

```sh
install -d -o root -g bitacora -m 0750 /etc/bitacora
chown root:bitacora /etc/bitacora/token
chmod 0640 /etc/bitacora/token
```

Then install and start the service:

1. Run `packaging/scripts/provision-user.sh`. It creates the `bitacora` system
   user and `/var/lib/bitacora/spool` as `root:bitacora` with mode `0750`.
2. Copy `packaging/systemd/bitacora-agent.service` to
   `/etc/systemd/system/bitacora-agent.service`.
3. Create `/etc/bitacora` as `root:bitacora` with mode `0750`. Copy
   `packaging/systemd/bitacora-agent.env.example` to
   `/etc/bitacora/agent.env`, set `BITACORA_HUB_URL` and
   `BITACORA_TOKEN_FILE`, then set the environment file mode to `0640`.
4. Write the enrollment token to the configured token path, owned by
   `root:bitacora` with mode `0640`. The enrollment command displayed by the
   hub creates those permissions automatically.
5. Run `systemctl daemon-reload` and
   `systemctl enable --now bitacora-agent.service`.
6. Verify the startup line with
   `journalctl -u bitacora-agent.service -b`. It names the hub, host ID, and
   number of enabled collectors; all agent log lines include timestamps.

Do not put `BITACORA_TOKEN` directly in the environment file. Keep the token
in the configured root-owned file so the service reads it without exposing it
in process environment inspection.

- `scripts/provision-user.sh` — creates the non-root `bitacora` system user,
  grants it `systemd-journal` membership, and creates
  `/var/lib/bitacora/spool` with the required `root:bitacora 0750`
  permissions. Idempotent, run as root.
- `systemd/bitacora-agent.service` — the main daemon's sandboxed unit,
  exactly as specified in ADR-0005. Never relax any of these settings
  without a matching ADR.
- `systemd/bitacora-smart.service` + `systemd/bitacora-smart.timer` — the
  first privileged helper: root, no network, dies within
  `RuntimeMaxSec=60`, runs every 15 minutes.
- `systemd/bitacora-vpn.service` + `systemd/bitacora-vpn.timer` — the
  second privileged helper (ADR-0016): root, `CAP_NET_ADMIN` for `wg`'s
  netlink queries, runs every 5 minutes. Unlike `bitacora-smart`, it does
  **not** set `PrivateNetwork=yes` — WireGuard interfaces are
  network-namespace scoped, so an isolated netns would see none of them.
- `rpm/` — AlmaLinux/RPM notes and the SELinux policy module source.
- `unraid/` — UnRaid plugin skeleton and rc.d service wrapper for hosts without
  systemd.

Check the result with `bita doctor`.

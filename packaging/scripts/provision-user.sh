#!/usr/bin/env bash
# Provisions the non-root bitacora system user, the group used by the
# systemd-journal supplementary group grant, its persistent state directory,
# and the inbound/outbound spool directories (ADR-0005, ADR-0008). Idempotent:
# safe to re-run.
#
# Run as root. Does not install any binary or systemd unit — see
# packaging/systemd/ for those.
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "provision-user.sh must run as root" >&2
  exit 1
fi

if ! id -u bitacora >/dev/null 2>&1; then
  useradd \
    --system \
    --no-create-home \
    --shell /usr/sbin/nologin \
    --comment "Bitácora agent" \
    bitacora
  echo "created system user bitacora"
else
  echo "system user bitacora already exists"
fi

if getent group systemd-journal >/dev/null 2>&1; then
  usermod -aG systemd-journal bitacora
else
  echo "warning: group systemd-journal not found — journald collector will be degraded" >&2
fi

# The agent creates host_id and persistent collector cursors in this directory.
install -d -o bitacora -g bitacora -m 0750 /var/lib/bitacora

# Privileged helpers write inbound entries here; the agent only reads them.
install -d -o root -g bitacora -m 0750 /var/lib/bitacora/spool

# The agent owns its outbound WAL so it can create and append segments without
# being able to write the root-owned inbound spool directory.
install -d -o bitacora -g bitacora -m 0750 /var/lib/bitacora/spool/outbound

echo "provisioned agent state, inbound spool, and outbound WAL directories"

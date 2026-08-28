#!/usr/bin/env bash
# Provisions the non-root bitacora system user, the group used by the
# systemd-journal supplementary group grant, and the /var/lib/bitacora/spool
# exchange directory (ADR-0005). Idempotent: safe to re-run.
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

install -d -o root -g bitacora -m 0750 /var/lib/bitacora
install -d -o root -g bitacora -m 0750 /var/lib/bitacora/spool

echo "provisioned /var/lib/bitacora and /var/lib/bitacora/spool (root:bitacora, 0750)"

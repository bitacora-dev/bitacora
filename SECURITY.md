# Security Policy

## Threat model

Bitácora agents run on machines the operator trusts but wants visibility
into, and push data over the network to a hub that stores it and serves an
API/UI. The interesting attackers are:

- **A network attacker between agent and hub**, or on the same network as the
  hub's listening interface.
- **Whoever compromises the hub itself**, since it's the piece that serves
  HTML and exposes a web API.
- **Untrusted input arriving through observed data**: container names, log
  lines, file paths, command output. This data is parsed and stored, never
  executed.

Out of scope: a physically compromised or already-rooted host — an attacker
with root on an observed machine can trivially feed the agent fake data or
kill it outright, and no agent-side design fixes that.

## The one hard limit: read-only, always

Bitácora **never executes commands derived from observed or remote data**,
on any machine it monitors. See
[ADR-0012](docs/adr/0012-solo-lectura.md) for the full decision and rationale.
Concretely, out of scope for the whole project as it stands:

- Arbitrary command execution, in any form.
- Any remote shell or terminal.
- Starting, stopping or restarting services, containers or units.
- Writing outside `/var/lib/bitacora` and `/etc/bitacora`.
- Installing or updating packages.
- Any action derived from observed data, without exception.

Privileged helpers (see
[ADR-0005](docs/adr/0005-modelo-de-privilegios.md)) run a closed,
code-defined list of read-only query commands with no parameters sourced
from external data. If a change would violate this boundary, it needs a new
ADR superseding ADR-0012 before it's implementable — see
[CONTRIBUTING.md](CONTRIBUTING.md).

## Other mechanical guarantees

- The agent runs as a non-root system user; only short-lived helpers touch
  root, and only for the closed command list above
  ([ADR-0005](docs/adr/0005-modelo-de-privilegios.md)).
- The ingest port never binds to `0.0.0.0` by default
  ([ADR-0002](docs/adr/0002-separacion-agente-hub.md)).
- `gitleaks` runs in CI (blocking) and as a pre-commit hook, alongside
  `detect-secrets`, so no password, token, private domain or private IP ever
  lands in this public repository.
- CI fails the build if `os/exec` appears outside the helper packages and
  `bitacora-run` ([ADR-0012](docs/adr/0012-solo-lectura.md)).

## Reporting a vulnerability

Please **do not** open a public issue for a security vulnerability. Instead,
use GitHub's private vulnerability reporting for this repository
(Security tab → "Report a vulnerability"). We aim to acknowledge reports
within 5 business days.

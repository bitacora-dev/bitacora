# Contributing to Bitácora

Thanks for your interest in Bitácora. The project is early-stage: architecture
is decided (see [`docs/adr/`](docs/adr/)), code is just starting.

## Before you write code

Read [`docs/adr/`](docs/adr/) first. ADRs are binding: if what you want to do
contradicts an accepted ADR, open a new ADR proposing the change instead of
working around it in code.

## Workflow

- Branches: `task/*` → PR → `development` → `main`.
- `development` is integration/staging. `main` is stable and tagged.
- Never push directly to `development` or `main`. Both are protected: CI must
  be green and at least one human approval is required.
- **No AI agent merges a PR.** Merging is always done by a human.
- Every PR, human or agent-authored, states in its description: which ADR(s)
  it implements, what was tested, and what was deliberately left out.
- Cross-review across agents/tools is encouraged (one implements, another
  audits, or vice versa) — there are no fixed reviewer roles.

## Commits

- [Conventional Commits](https://www.conventionalcommits.org/), in English,
  from the very first commit — commit history is the one thing that can't be
  rewritten later without cost.
- No AI attribution in commit messages or trailers.

## Code

- Go 1.22+ for `bitacora-agent`, `bitacora-hub`, `bita`. TypeScript/React for
  the frontend. See [ADR-0001](docs/adr/0001-lenguajes-y-stack.md).
- Code, identifiers and comments are in English. ADRs stay in Spanish for now
  (see [ADR-0013](docs/adr/0013-nombre-licencia-y-gobernanza.md)).
- `gofmt` and `go vet` must be clean before opening a PR.
- No dependency that requires CGO by default in the agent.
- License boundary: Apache-2.0 code may only import Apache-2.0 code inside
  this repository, plus the Go standard library and compatible external
  dependencies. AGPL-3.0 core code may import Apache-2.0 internal contracts
  and adapters, but not the other way around. See
  [ADR-0018](docs/adr/0018-frontera-licencia-bitacora-run.md).

## Security-sensitive changes

Bitácora is read-only by design ([ADR-0012](docs/adr/0012-solo-lectura.md)).
Any change that adds command execution, remote shells, or file writes outside
`/var/lib/bitacora` and `/etc/bitacora` is out of scope unless it's a
privileged helper following [ADR-0005](docs/adr/0005-modelo-de-privilegios.md).
See [SECURITY.md](SECURITY.md) for how to report a vulnerability instead of
opening a public issue.

## License

Bitácora core (`bitacora-hub`, `bitacora-agent`, `bita`, web UI) is AGPL-3.0.
`bitacora-run` and its direct internal support contracts/adapters are
Apache-2.0. By contributing, you agree your contribution is licensed under the
license of the directory it lands in — see
[ADR-0013](docs/adr/0013-nombre-licencia-y-gobernanza.md) and
[ADR-0018](docs/adr/0018-frontera-licencia-bitacora-run.md).

# web

The hub's single-page dashboard (ADR-0001: React + Vite + TypeScript +
TailwindCSS + uPlot). One page, no router: `GET /v1/summary?host_id=...`
returns everything it needs in one call (ADR-0014).

## Developing

```sh
npm ci
npm run dev
```

Runs against a real hub on `127.0.0.1:8081` (proxied — see `vite.config.ts`).
Start `bitacora-hub` separately.

## Building

```sh
npm run build
```

Outputs straight into `../internal/webui/dist`, which `go:embed`s it into
the `bitacora-hub` binary (`internal/webui/embed.go`). **The build output
is committed** — like `proto/bitacorapb`, so `go build` never needs Node
installed. After changing anything under `src/`, rebuild and commit the
result:

```sh
npm run build
git add ../internal/webui/dist
```

CI rebuilds the frontend and verifies that the committed output is current.
Keeping `dist/` versioned still lets local Go builds run without Node.

## Resolving generated asset conflicts

`dist/` remains committed because the hub embeds it and therefore Go builds do
not require Node. Its hashed output is not meaningful to merge line by line.
Enable the repository-local merge driver once in every clone:

```sh
./scripts/git/configure-merge-drivers.sh
```

For conflicts under `internal/webui/dist/**`, the driver keeps the current
branch's version. After the merge, regenerate the asset and commit the result:

```sh
cd web
npm ci
npm run build
git add ../internal/webui/dist
```

The driver is intentionally local Git configuration, so it is never activated
silently for contributors. Re-run the setup script after moving a checkout or
if `.git/config` is recreated.

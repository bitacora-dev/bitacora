# web

The hub's single-page dashboard (ADR-0001: React + Vite + TypeScript +
TailwindCSS + uPlot). One page, no router: `GET /v1/summary?host_id=...`
returns everything it needs in one call (ADR-0014).

## Developing

```sh
npm install
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

If the frontend outgrows this (larger bundles, frequent changes), moving
the build to CI instead of committing the output is a reasonable followup
— not done now, to keep the CI job Node-free while the project is small.

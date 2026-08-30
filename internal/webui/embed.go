// Package webui embeds the built frontend (ADR-0001: React + Vite +
// TailwindCSS + uPlot) into the bitacora-hub binary via go:embed.
//
// The source lives in ../../web. Rebuild and commit the output here with:
//
//	cd web && npm ci && npm run build
//
// See web/README.md.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var distFS embed.FS

// FS returns the built frontend, rooted so index.html sits at the top
// level (embed.FS otherwise keeps the "dist/" path prefix).
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// dist/ is committed to the repo; this can only fail if that
		// invariant is broken, which is a build-time bug, not a runtime
		// condition to handle gracefully.
		panic(err)
	}
	return sub
}

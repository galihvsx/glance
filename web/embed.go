// Package webassets embeds the built React SPA (web/dist) into the Go
// binary. Run `npm run build` in web/ BEFORE `go build` — the embed
// fails at compile time if dist/ is missing (dist is a build artifact,
// gitignored; the Dockerfile builds web inside the image).
package webassets

import "embed"

// Dist holds the built SPA files, rooted under "dist/".
// Serve them with fs.Sub(Dist, "dist").
var (
	//go:embed all:dist
	Dist embed.FS
)

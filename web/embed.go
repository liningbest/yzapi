// Package web embeds the built frontend (web/dist) into the binary.
package web

import "embed"

// Dist holds the compiled SPA. Run `pnpm build` in ./web before `go build`.
//
//go:embed all:dist
var Dist embed.FS

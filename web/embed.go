// Package web embeds the built frontend distribution for production deployment.
// In development, use `cd web && npm run dev` to start the Vite dev server with HMR.
// In production, the Go binary serves the embedded dist/ files directly.
package web

import "embed"

//go:embed dist
var Dist embed.FS
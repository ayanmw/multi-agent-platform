// Package web embeds the built frontend distribution for production deployment.
// In development, use `cd web && npm run dev` to start the Vite dev server with HMR.
// In production, the Go binary serves the embedded dist/ files directly.
//
// v1 and v2 UI builds are both embedded so the active version can be selected at
// runtime via the UI_VERSION environment variable (default "v1").
package web

import "embed"

// Dist is the primary (v1) frontend build output embedded from web/dist.
//go:embed dist
var Dist embed.FS

// V2Dist is the secondary (v2) frontend build output embedded from web/v2/dist.
// It is used when UI_VERSION is set to "v2".
//go:embed v2/dist
var V2Dist embed.FS

// Package web embeds the built frontend distributions for production deployment.
// In development, use `cd web && npm run dev` or `cd web/v2 && npm run dev`.
// In production, the Go binary serves the embedded dist/ files directly.
//
// UI versions are served under versioned paths:
//   /          -> latest default version
//   /ui/v1/    -> v1 (classic)
//   /ui/v2/    -> v2 (control room)
// Future versions can be added by embedding web/v{N}/dist and registering in UIVersions.
package web

import "embed"

// Dist is the v1 frontend build output embedded from web/dist.
//go:embed dist
var Dist embed.FS

// V2Dist is the v2 frontend build output embedded from web/v2/dist.
//go:embed v2/dist
var V2Dist embed.FS

// DefaultUIVersion is the version served at the root path "/".
// It should always point to the latest stable UI version.
const DefaultUIVersion = "v2"

// UIVersions maps a version ID to its embedded distribution.
type UIVersions struct {
	FS     embed.FS
	SubDir string
}

// UIVersionsRegistry 提供所有可访问的历史版本。
// 新增版本时只需在这里注册，无需改其他代码。
var UIVersionsRegistry = map[string]UIVersions{
	"v1": {FS: Dist, SubDir: "dist"},
	"v2": {FS: V2Dist, SubDir: "v2/dist"},
}

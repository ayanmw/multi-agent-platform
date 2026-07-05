// Package version provides the application version string, read from version.txt
// in this directory. This is the single source of truth for version information
// across all modules (Go backend, Vue frontend, HTML docs).
//
// Usage:
//
//	import "github.com/anmingwei/multi-agent-platform/internal/version"
//	fmt.Println(version.Version) // "v0.4 Alpha"
//
// The version is embedded at compile time via go:embed.
// The frontend reads it from the /api/version endpoint at runtime.
// HTML docs should be updated to match this version (see update-doc-versions.sh).
package version

import (
	_ "embed"
	"strings"
)

// Version is the application version string, trimmed from version.txt.
// It is embedded at compile time via go:embed.
//
//go:embed version.txt
var rawVersion string

// Version is the trimmed version string (no trailing newline).
var Version = strings.TrimSpace(rawVersion)
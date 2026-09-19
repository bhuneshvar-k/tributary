// Package version provides build-time version information for tributary.
//
// These variables are set via ldflags at build time:
//
//	go build -ldflags "-X github.com/bhuneshvar-k/tributary/internal/version.version=v1.0.0"
//
// When built without ldflags (e.g., during development), they default to
// sensible fallback values that identify the build as a development version.
package version

import (
	"fmt"
	"runtime"
)

// These variables are set at build time via -ldflags.
var (
	// version is the semantic version string (e.g., "v1.0.0").
	// Set to "dev" when building without ldflags.
	version = "dev"

	// commit is the git commit hash.
	// Set to "none" when building without ldflags.
	commit = "none"

	// date is the build date in RFC3339 format.
	// Set to "unknown" when building without ldflags.
	date = "unknown"

	// builtBy identifies who/what built the binary.
	builtBy = "unknown"
)

// Version returns the full version string.
func Version() string {
	return version
}

// Commit returns the git commit hash.
func Commit() string {
	return commit
}

// Date returns the build date.
func Date() string {
	return date
}

// BuiltBy returns who built the binary.
func BuiltBy() string {
	return builtBy
}

// String returns a formatted version string suitable for display.
// Format: tributary v1.0.0 (commit: abc1234, built: 2026-09-19)
func String() string {
	return fmt.Sprintf("tributary %s (commit: %s, built: %s)", version, commit, date)
}

// Short returns a short version string.
// Format: v1.0.0
func Short() string {
	return version
}

// GoVersion returns the Go version used to build the binary.
func GoVersion() string {
	return runtime.Version()
}

// Platform returns the OS and architecture.
func Platform() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}

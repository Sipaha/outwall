// Package version exposes the build version string.
package version

// version is overridden at build time via -ldflags "-X .../version.version=...".
var version = "0.1.0-dev"

// String returns the current build version.
func String() string { return version }

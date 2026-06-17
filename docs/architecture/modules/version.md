# module: internal/version

Holds the build version string, overridable at link time via
`-ldflags "-X github.com/Sipaha/outwall/internal/version.version=..."`.

## Public API

- `String() string` — returns the current build version (defaults to `0.1.0-dev`).

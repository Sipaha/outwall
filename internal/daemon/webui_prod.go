//go:build prod

package daemon

import "embed"

// Release build (-tags prod): embed the real Vite bundle from webdist/ (gitignored; produced by
// `make build-web`). The prod tag is set by the release Makefile targets (build / run-server /
// build-desktop), each of which runs build-web first, so webdist/ is populated at compile time.
// A bare `go build -tags prod` without a prior web build will fail — that is intentional; use the
// Makefile targets. Default builds use webui_dev.go (the tracked webseed/ placeholder).
//
//go:embed all:webdist
var uiFS embed.FS

const uiRoot = "webdist"

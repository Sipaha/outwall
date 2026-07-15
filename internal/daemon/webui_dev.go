//go:build !prod

package daemon

import "embed"

// Dev/default build: embed the tracked webseed/ placeholder. This lets `go build`, `go vet`,
// `go test`, `make build-fast` and `make install` produce a working binary without a web build.
// Vite never writes to webseed/, so there is no git churn. Release builds use webui_prod.go
// (-tags prod) to embed the real bundle instead.
//
//go:embed all:webseed
var uiFS embed.FS

const uiRoot = "webseed"

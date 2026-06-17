BINDIR := dist/bin
BIN := $(BINDIR)/outwall

GO_LDFLAGS := -X github.com/Sipaha/outwall/internal/version.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)

.PHONY: build build-fast build-web test fmt vet tidy

# Full build: rebuild the web UI first (its output lands in internal/daemon/webdist,
# which the Go binary embeds via //go:embed), then compile the binary.
build: build-web build-fast

# build-fast skips the web rebuild and just compiles the Go binary against whatever
# webdist currently holds (the committed placeholder, or a prior `make build-web`).
build-fast:
	@mkdir -p $(BINDIR)
	CGO_ENABLED=0 go build -ldflags "$(GO_LDFLAGS)" -o $(BIN) ./cmd/outwall

# build-web installs web deps and produces the production bundle into
# internal/daemon/webdist (vite build.outDir, emptyOutDir).
build-web:
	pnpm -C web install --frozen-lockfile=false
	pnpm -C web build

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

tidy:
	go mod tidy

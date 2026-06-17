BINDIR := dist/bin
BIN := $(BINDIR)/outwall
DESKTOP := $(BINDIR)/outwall-desktop

GO_LDFLAGS := -X github.com/Sipaha/outwall/internal/version.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)

# Build tag(s) for the desktop (Wails + GTK/WebKit, CGO) target. The `desktop`
# tag gates cmd/outwall-desktop out of the default no-tag gate so the server
# binary stays CGO-free.
DESKTOP_TAGS ?= desktop

.PHONY: build build-fast build-web build-desktop test fmt vet tidy

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

# build-desktop compiles the Wails v3 GUI shell (CGO + GTK3 + WebKit2GTK 4.1).
# It does NOT force a web rebuild — like build-fast it embeds whatever webdist
# currently holds (the committed bundle, or a prior `make build-web`) — so the
# desktop build stays self-contained and doesn't require pnpm. Run `make
# build-web` first if you need a fresh UI bundle.
build-desktop:
	@mkdir -p $(BINDIR)
	CGO_ENABLED=1 go build -tags "$(DESKTOP_TAGS)" -ldflags "$(GO_LDFLAGS)" -o $(DESKTOP) ./cmd/outwall-desktop

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

tidy:
	go mod tidy

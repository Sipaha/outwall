BINDIR := dist/bin
BIN := $(BINDIR)/outwall
DESKTOP := $(BINDIR)/outwall-desktop

# Where `make install` links the `outwall` CLI so agents can invoke it from any directory.
# Override: `make install DESTBIN=/usr/local/bin` (or `PREFIX=/usr/local`).
PREFIX ?= $(HOME)/.local
DESTBIN ?= $(PREFIX)/bin

GO_LDFLAGS := -X github.com/Sipaha/outwall/internal/version.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)

# Build tag(s) for the desktop (Wails + GTK/WebKit, CGO) target. The `desktop`
# tag gates cmd/outwall-desktop out of the default no-tag gate so the server
# binary stays CGO-free.
DESKTOP_TAGS ?= desktop

# SERVER_TAGS gates the embedded web UI (internal/daemon/webui_{dev,prod}.go). Empty (default) =
# embed the tracked webseed/ placeholder — no web build needed, so bare/`build-fast`/`install`/
# `check` compile offline. Release targets set it to `prod` (below) to embed the real webdist/
# bundle after build-web. This split keeps the web build from ever dirtying a tracked file.
SERVER_TAGS ?=

.PHONY: build build-fast build-web build-desktop build-desktop-fast run run-server test fmt vet tidy \
        check lint lint-go lint-web test-web fmt-check web-deps install uninstall

# Full build: rebuild the web UI first (its output lands in the gitignored internal/daemon/webdist),
# then compile the binary with -tags prod so it embeds that real bundle (webui_prod.go).
build: SERVER_TAGS = prod
build: build-web build-fast

# build-fast skips the web rebuild and just compiles the Go binary. With the default empty
# SERVER_TAGS it embeds the tracked webseed/ placeholder (no web build required); `build` sets
# SERVER_TAGS=prod to embed the real webdist/ bundle instead.
build-fast:
	@mkdir -p $(BINDIR)
	CGO_ENABLED=0 go build -tags "$(SERVER_TAGS)" -ldflags "$(GO_LDFLAGS)" -o $(BIN) ./cmd/outwall

# build-web installs web deps and produces the production bundle into
# internal/daemon/webdist (vite build.outDir, emptyOutDir).
build-web:
	pnpm -C web install --frozen-lockfile=false
	pnpm -C web build

# build-desktop compiles the Wails v3 GUI shell (CGO + GTK/WebKit). It rebuilds
# the web UI first (build-web) so the embedded bundle is the REAL one (via -tags prod),
# not the webseed/ placeholder — a shipped desktop app must contain a working UI. Use
# build-desktop-fast to skip the web rebuild against an existing webdist (e.g. CI that
# built the web in a prior step / has no pnpm); it also embeds the real bundle (prod).
build-desktop: build-web build-desktop-fast

build-desktop-fast:
	@mkdir -p $(BINDIR)
	CGO_ENABLED=1 go build -tags "$(DESKTOP_TAGS) prod" -ldflags "$(GO_LDFLAGS)" -o $(DESKTOP) ./cmd/outwall-desktop

# run rebuilds everything (web bundle + Wails desktop binary) so all code changes are
# picked up, then launches the desktop app.
run: build-desktop
	$(DESKTOP)

# run-server rebuilds the CGO-free server+CLI (web bundle included) and runs the daemon in
# the foreground (UI at http://127.0.0.1:8182/). Convenience for UI work without the webview.
run-server: build
	$(BIN) serve

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

tidy:
	go mod tidy

# ---- Full pre-release gate -------------------------------------------------
# `make check` is the single command every agent runs before a release/merge. It runs BOTH sides:
# Go (gofmt-check, go vet, golangci-lint, go test, CGO-free build) and web (eslint, tsc typecheck,
# vitest). If any linter/test/build fails, check fails. Individual pieces are runnable on their own.
check: fmt-check vet lint test test-web build-fast
	@echo "✓ make check passed — gofmt, vet, golangci-lint, eslint, tsc, go test, vitest, build"

# lint runs every configured linter, Go and web.
lint: lint-go lint-web

# fmt-check fails (non-zero) if any Go file is not gofmt-clean, without modifying files.
fmt-check:
	@files=$$(gofmt -l .); if [ -n "$$files" ]; then echo "gofmt needed on:"; echo "$$files"; exit 1; fi

# lint-go runs golangci-lint (config: .golangci.yml). Required — install if missing.
lint-go:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed (required by make lint/check) — https://golangci-lint.run/welcome/install/"; exit 1; }
	golangci-lint run ./...

# web-deps installs the web toolchain once; lint-web and test-web depend on it (make dedups it).
web-deps:
	pnpm -C web install --frozen-lockfile=false

# lint-web runs eslint + the TypeScript typecheck (tsc --noEmit) over the SPA.
lint-web: web-deps
	pnpm -C web lint
	pnpm -C web exec tsc --noEmit

# test-web runs the vitest unit suite for the SPA.
test-web: web-deps
	pnpm -C web exec vitest run

# ---- Install the CLI on PATH ----------------------------------------------
# `make install` builds the server+CLI binary and symlinks it into DESTBIN so agents can run
# `outwall <cmd>` (list-upstreams, request-preset, whoami, get-access, …) from any directory — the
# direct, MCP-free control plane. The symlink tracks dist/bin/outwall, so a later `make build`
# refreshes it automatically (no re-install). Depends on build-fast (the CLI subcommands don't need
# the embedded web UI; run `make build` first if you also want the installed `outwall serve` UI).
install: build-fast
	@mkdir -p $(DESTBIN)
	ln -sf $(abspath $(BIN)) $(DESTBIN)/outwall
	@echo "linked $(DESTBIN)/outwall -> $(abspath $(BIN))"
	@case ":$$PATH:" in *":$(DESTBIN):"*) echo "$(DESTBIN) is on PATH — try: outwall whoami" ;; \
	  *) echo "NOTE: $(DESTBIN) is NOT on PATH. Add it, e.g.: echo 'export PATH=\"$(DESTBIN):\$$PATH\"' >> ~/.profile" ;; esac

uninstall:
	rm -f $(DESTBIN)/outwall
	@echo "removed $(DESTBIN)/outwall"

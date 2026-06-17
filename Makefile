BINDIR := dist/bin
BIN := $(BINDIR)/outwall

.PHONY: build test fmt vet tidy
build:
	@mkdir -p $(BINDIR)
	CGO_ENABLED=0 go build -ldflags "-X github.com/Sipaha/outwall/internal/version.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)" -o $(BIN) ./cmd/outwall

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

tidy:
	go mod tidy

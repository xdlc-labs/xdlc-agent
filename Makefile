.PHONY: test lint validate build bench check-versions ui

# Exclude ui/node_modules from Go package walk. Recursive (=) so `go list`
# only runs for targets that use it, not on every make invocation.
GO_PKGS = $(shell go list ./... | grep -v /node_modules/)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

test:
	go test $(GO_PKGS) -race

bench:
	go test $(GO_PKGS) -run=^$$ -bench=. -benchmem

lint:
	golangci-lint run ./...

validate:
	go run ./cmd/xdlc-agent validate --config config.example.yaml --gitops-dir ""

check-versions:
	./scripts/check-version-refs.sh

# Same flags as .goreleaser.yml / deploy/Dockerfile: stripped, reproducible paths.
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/xdlc ./cmd/xdlc-agent

# Rebuild the ops console and refresh the go:embed copy.
ui:
	cd ui && bun install --frozen-lockfile && bun run build
	find internal/console/dist -mindepth 1 -delete
	cp -a ui/dist/. internal/console/dist/

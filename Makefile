.PHONY: test lint validate build

# Exclude ui/node_modules from Go package walk.
GO_PKGS := $(shell go list ./... | grep -v /node_modules/)

test:
	go test $(GO_PKGS) -race

lint:
	golangci-lint run ./...

validate:
	go run ./cmd/xdlc-agent validate --config config.example.yaml --gitops-dir ""

build:
	go build -o bin/xdlc-agent ./cmd/xdlc-agent

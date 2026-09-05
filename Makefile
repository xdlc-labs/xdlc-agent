.PHONY: setup bootstrap bootstrap-minikube test lint lint-helm validate build terraform-validate check-links

setup:
	./scripts/setup.sh

bootstrap:
	./scripts/bootstrap-local.sh

bootstrap-minikube:
	./scripts/bootstrap-minikube.sh

# Exclude ui/node_modules (and any nested node_modules) from Go package walk.
GO_PKGS := $(shell go list ./... | grep -v /node_modules/)

test:
	go test $(GO_PKGS) -race

lint:
	golangci-lint run ./...

validate:
	go run ./cmd/xdlc-agent validate --config config.example.yaml --gitops-dir gitops

lint-helm:
	helm lint deploy/helm/xdlc-agent
	helm lint helm/service-template --set image.repository=ghcr.io/x/y --set image.tag=v1
	helm template test deploy/helm/xdlc-agent > /dev/null
	helm template test helm/service-template \
		-f gitops/values/dev.yaml -f gitops/values/dev/example-service.yaml > /dev/null

build:
	go build -o bin/xdlc-agent ./cmd/xdlc-agent

terraform-validate:
	./scripts/terraform-validate.sh

# Relative markdown links + #anchors across all tracked *.md.
check-links:
	./scripts/check-links.sh

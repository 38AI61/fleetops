-include deploy/.local/tools.mk

GO ?= go
GOFMT ?= gofmt
GOLANGCI_LINT ?= golangci-lint
MIGRATE ?= migrate

GOLANGCI_LINT_VERSION := v2.15.0
MIGRATE_VERSION := v4.18.3
MIGRATE_ARGS ?= up

.PHONY: help tools fmt-check lint test migrate seed seed-demo
.DEFAULT_GOAL := help

help: ## list targets
	@grep -hE '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | sed -E 's/:.*## /\t/' | awk -F'\t' '{printf "  %-10s %s\n", $$1, $$2}'

tools: ## install pinned golangci-lint and migrate CLIs into GOPATH/bin
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(GO) install -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)

fmt-check: ## fail if any Go file is not gofmt-clean
	@out=$$($(GOFMT) -l .); if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

lint: fmt-check ## gofmt check, then golangci-lint
	$(GOLANGCI_LINT) run

test: ## run all Go tests with the race detector
	$(GO) test ./... -race -count=1

migrate: ## apply migrations (MIGRATE_ARGS="up" default; e.g. MIGRATE_ARGS="down 1")
	@if [ -z "$$DATABASE_URL" ]; then echo "DATABASE_URL is not set" >&2; exit 2; fi
	$(MIGRATE) -path db/migrations -database "$$DATABASE_URL" $(MIGRATE_ARGS)

seed: ## stub: demo seed lands in P2
	@echo "seed: tools/seed lands in P2 (D-05)" >&2; exit 1

seed-demo: ## stub: demo fleet seed lands in P23
	@echo "seed-demo: lands in P23 (roadmap)" >&2; exit 1

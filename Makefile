-include deploy/.local/tools.mk

GO ?= go
GOFMT ?= gofmt
GOLANGCI_LINT ?= golangci-lint
MIGRATE ?= migrate

GOLANGCI_LINT_VERSION := v2.15.0
MIGRATE_VERSION := v4.18.3
MIGRATE_ARGS ?= up

.PHONY: help tools fmt-check lint test migrate seed seed-demo hub-scale e2e e2e-stack-up e2e-stack-down e2e-test
.DEFAULT_GOAL := help

help: ## list targets
	@grep -hE '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | sed -E 's/:.*## /\t/' | awk -F'\t' '{printf "  %-10s %s\n", $$1, $$2}'

tools: ## install pinned golangci-lint and migrate CLIs into GOPATH/bin
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(GO) install -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)

fmt-check: ## fail if any Go file is not gofmt-clean
	@out=$$($(GOFMT) -l .); if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

lint: fmt-check ## gofmt check, then golangci-lint
	@PATH="$$(dirname "$$(command -v $(GO))"):$$PATH" $(GOLANGCI_LINT) run

test: ## run all Go tests with the race detector
	$(GO) test ./... -race -count=1

migrate: ## apply migrations (MIGRATE_ARGS="up" default; e.g. MIGRATE_ARGS="down 1")
	@if [ -z "$$DATABASE_URL" ]; then echo "DATABASE_URL is not set" >&2; exit 2; fi
	$(MIGRATE) -path db/migrations -database "$$DATABASE_URL" $(MIGRATE_ARGS)

seed: ## bootstrap first fleet + admin (D-05); SEED_ARGS="--admin-email you@example.com"
	$(GO) run ./tools/seed $(SEED_ARGS)

seed-demo: ## seed fleet, admin, 50 demo vans, devices and demo drivers (first seed only); SEED_ARGS="--admin-email you@example.com"
	$(GO) run ./tools/seed --demo-devices $(SEED_ARGS)

hub-scale: ## run 10k synthetic WS connections under -race (GF-343; override count with SCALE_CONNS)
	$(GO) test ./internal/hub -race -count=1 -timeout 30m -run TestScaleConnections -args -scale.conns=$(or $(SCALE_CONNS),10000)

e2e: ## happy-path E2E: stack up, Playwright spec, stack down (CI calls this)
	@trap '$(MAKE) --no-print-directory e2e-stack-down' EXIT; $(MAKE) --no-print-directory e2e-stack-up && $(MAKE) --no-print-directory e2e-test

e2e-stack-up: ## reset compose, migrate, seed, start Go services and Vite servers (e2e/scripts/stack.sh)
	@GO="$(GO)" bash e2e/scripts/stack.sh up

e2e-stack-down: ## stop e2e services and compose (keeps volumes); verify ports 10010-10013 are free
	@bash e2e/scripts/stack.sh down

e2e-test: ## run the Playwright spec against a running stack (sources deploy/.local/e2e.env)
	@cd e2e && set -a && . ../deploy/.local/e2e.env && set +a && npx playwright test

-include deploy/.local/tools.mk

GO ?= go
GOFMT ?= gofmt
GOLANGCI_LINT ?= golangci-lint
MIGRATE ?= migrate
K6 ?= k6

GOLANGCI_LINT_VERSION := v2.15.0
MIGRATE_VERSION := v4.18.3
MIGRATE_ARGS ?= up

.PHONY: help tools fmt-check lint test migrate seed seed-demo hub-scale loadtest loadtest-ws loadtest-assign loadtest-report loadtest-ingest-steady loadtest-ingest-burst loadtest-static e2e e2e-stack-up e2e-stack-down e2e-test cloud-image
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

cloud-image: ## build the merged cloud container (deploy/cloud/Dockerfile.render) as fleetops:cloud
	docker build -f deploy/cloud/Dockerfile.render -t fleetops:cloud .

migrate: ## apply migrations (MIGRATE_ARGS="up" default; e.g. MIGRATE_ARGS="down 1")
	@if [ -z "$$DATABASE_URL" ]; then echo "DATABASE_URL is not set" >&2; exit 2; fi
	$(MIGRATE) -path db/migrations -database "$$DATABASE_URL" $(MIGRATE_ARGS)

seed: ## bootstrap first fleet + admin (D-05); SEED_ARGS="--admin-email you@example.com"
	$(GO) run ./tools/seed $(SEED_ARGS)

seed-demo: ## seed fleet, admin, 50 demo vans, devices and demo drivers (first seed only); SEED_ARGS="--admin-email you@example.com"
	$(GO) run ./tools/seed --demo-devices $(SEED_ARGS)

hub-scale: ## run 10k synthetic WS connections under -race (GF-343; override count with SCALE_CONNS)
	$(GO) test ./internal/hub -race -count=1 -timeout 30m -run TestScaleConnections -args -scale.conns=$(or $(SCALE_CONNS),10000)

# k6 scenarios (GF-337, GF-338). Needs K6 on PATH and TOKEN (admin access JWT).
K6_DIR := tools/loadtest/k6
LOADTEST_OUT := deploy/.local/loadtest

loadtest: loadtest-ws loadtest-assign loadtest-report loadtest-ingest-steady loadtest-ingest-burst ## run all five k6 scenarios in order (K6, TOKEN)

loadtest-ws: ## k6: WS fanout, freshness p95 <= 2 s
	@mkdir -p $(LOADTEST_OUT) && $(K6) run --summary-export $(LOADTEST_OUT)/ws-fanout.json -e TOKEN="$$TOKEN" $(K6_DIR)/ws-fanout.js

loadtest-assign: ## k6: assign latency, p95 <= 300 ms
	@mkdir -p $(LOADTEST_OUT) && $(K6) run --summary-export $(LOADTEST_OUT)/assign.json -e TOKEN="$$TOKEN" $(K6_DIR)/assign.js

loadtest-report: ## k6: daily report latency, p95 <= 300 ms
	@mkdir -p $(LOADTEST_OUT) && $(K6) run --summary-export $(LOADTEST_OUT)/report.json -e TOKEN="$$TOKEN" $(K6_DIR)/report.js

loadtest-ingest-steady: ## k6: ingest 100 msg/s steady state
	@mkdir -p $(LOADTEST_OUT) && $(K6) run --summary-export $(LOADTEST_OUT)/ingest-steady.json $(K6_DIR)/ingest-steady.js

loadtest-ingest-burst: ## k6: ingest burst to 2,000 msg/s with heap check
	@mkdir -p $(LOADTEST_OUT) && $(K6) run --summary-export $(LOADTEST_OUT)/ingest-burst.json $(K6_DIR)/ingest-burst.js

loadtest-static: ## validate k6 scripts without k6 (thresholds, balance, node syntax)
	$(GO) test ./tools/loadtest/... -count=1

e2e: ## happy-path E2E: stack up, Playwright spec, stack down (CI calls this)
	@trap '$(MAKE) --no-print-directory e2e-stack-down' EXIT; $(MAKE) --no-print-directory e2e-stack-up && $(MAKE) --no-print-directory e2e-test

e2e-stack-up: ## reset compose, migrate, seed, start Go services and Vite servers (e2e/scripts/stack.sh)
	@GO="$(GO)" bash e2e/scripts/stack.sh up

e2e-stack-down: ## stop e2e services and compose (keeps volumes); verify ports 10010-10013 are free
	@bash e2e/scripts/stack.sh down

e2e-test: ## run the Playwright spec against a running stack (sources deploy/.local/e2e.env)
	@cd e2e && set -a && . ../deploy/.local/e2e.env && set +a && npx playwright test

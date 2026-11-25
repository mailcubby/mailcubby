.DEFAULT_GOAL := help

SHELL := /bin/sh

SQLC_VERSION := 1.31.1
GOLANGCI_LINT_VERSION := 2.15.0

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Deterministic dev key (32 zero bytes base64) and bootstrap admin for local server-mode development only.
DEV_APP_KEY := AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
DEV_ADMIN_EMAIL ?= admin@example.test
DEV_ADMIN_PASSWORD ?= devpassword123

MODE ?= local
SMTP ?= localhost:1025
USER ?= default
COUNT ?= 1

.PHONY: help tools deps generate web build build-all run run-server dev \
	test test-race test-web lint fmt check bench e2e seed docker migration clean

help: ## List available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

tools: ## Download pinned sqlc and golangci-lint binaries into ./bin/
	@set -eu; \
	mkdir -p bin; \
	os=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	arch=$$(uname -m); \
	case "$$arch" in \
		x86_64|amd64) arch=amd64 ;; \
		arm64|aarch64) arch=arm64 ;; \
		*) echo "unsupported architecture: $$arch" >&2; exit 1 ;; \
	esac; \
	if [ ! -x bin/sqlc ] || ! bin/sqlc version 2>/dev/null | grep -q "v$(SQLC_VERSION)"; then \
		echo "Downloading sqlc v$(SQLC_VERSION) ($$os/$$arch)..."; \
		tmp=$$(mktemp -d); \
		trap 'rm -rf "$$tmp"' EXIT; \
		curl -fsSL "https://github.com/sqlc-dev/sqlc/releases/download/v$(SQLC_VERSION)/sqlc_$(SQLC_VERSION)_$${os}_$${arch}.tar.gz" \
			| tar -xz -C "$$tmp" sqlc; \
		mv "$$tmp/sqlc" bin/sqlc; \
		chmod +x bin/sqlc; \
		rm -rf "$$tmp"; \
		trap - EXIT; \
	else \
		echo "bin/sqlc v$(SQLC_VERSION) already installed"; \
	fi; \
	if [ ! -x bin/golangci-lint ] || ! bin/golangci-lint version 2>/dev/null | grep -q "$(GOLANGCI_LINT_VERSION)"; then \
		echo "Downloading golangci-lint v$(GOLANGCI_LINT_VERSION) ($$os/$$arch)..."; \
		tmp=$$(mktemp -d); \
		trap 'rm -rf "$$tmp"' EXIT; \
		curl -fsSL "https://github.com/golangci/golangci-lint/releases/download/v$(GOLANGCI_LINT_VERSION)/golangci-lint-$(GOLANGCI_LINT_VERSION)-$${os}-$${arch}.tar.gz" \
			| tar -xz -C "$$tmp" --strip-components=1 "golangci-lint-$(GOLANGCI_LINT_VERSION)-$${os}-$${arch}/golangci-lint"; \
		mv "$$tmp/golangci-lint" bin/golangci-lint; \
		chmod +x bin/golangci-lint; \
		rm -rf "$$tmp"; \
		trap - EXIT; \
	else \
		echo "bin/golangci-lint v$(GOLANGCI_LINT_VERSION) already installed"; \
	fi

deps: ## Download Go modules, web packages, and Playwright Chromium (once configured)
	go mod download
	@if [ -f web/package.json ]; then \
		pnpm --dir web install --frozen-lockfile; \
	else \
		echo "deps: skipping pnpm install (web/package.json does not exist yet)"; \
	fi
	@if [ -f web/playwright.config.ts ]; then \
		pnpm --dir web exec playwright install chromium; \
	else \
		echo "deps: skipping Playwright Chromium install (web/playwright.config.ts does not exist yet)"; \
	fi

generate: ## Run sqlc and OpenAPI TypeScript code generation
	@if [ -f sqlc.yaml ]; then \
		test -x ./bin/sqlc || { echo "bin/sqlc not found: run 'make tools' first" >&2; exit 1; }; \
		./bin/sqlc generate; \
	else \
		echo "generate: skipping sqlc (sqlc.yaml does not exist yet)"; \
	fi
	@if [ -f internal/api/openapi.yaml ]; then \
		pnpm --dir web gen:api; \
	else \
		echo "generate: skipping OpenAPI TypeScript types (internal/api/openapi.yaml does not exist yet)"; \
	fi

web: ## Build the React UI and sync into internal/web/dist/
	pnpm --dir web install --frozen-lockfile
	pnpm --dir web build
	@mkdir -p internal/web/dist
	@find internal/web/dist -mindepth 1 -maxdepth 1 ! -name '.gitkeep' -exec rm -rf {} +
	@cp -R web/dist/. internal/web/dist/

build: ## Build static Go binary to bin/mailcubby
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/mailcubby ./cmd/mailcubby

build-all: web build ## Build web UI and static Go binary

run: ## Run Mailcubby in local mode with data in ./data
	MAILCUBBY_DATA_DIR=./data go run ./cmd/mailcubby serve

run-server: ## Run Mailcubby in server mode for development (data in ./data-server)
	MAILCUBBY_MODE=server \
	MAILCUBBY_APP_KEY=$(DEV_APP_KEY) \
	MAILCUBBY_BASE_URL=http://localhost:8025 \
	MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH=true \
	MAILCUBBY_ADMIN_EMAIL=$(DEV_ADMIN_EMAIL) \
	MAILCUBBY_ADMIN_PASSWORD=$(DEV_ADMIN_PASSWORD) \
	MAILCUBBY_DATA_DIR=./data-server \
	go run ./cmd/mailcubby serve

dev: ## Run Go server and Vite dev server on :5173 (MODE=local|server)
	@set -eu; \
	if [ "$(MODE)" = "server" ]; then \
		MAILCUBBY_MODE=server \
		MAILCUBBY_APP_KEY=$(DEV_APP_KEY) \
		MAILCUBBY_BASE_URL=http://localhost:5173 \
		MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH=true \
		MAILCUBBY_ADMIN_EMAIL=$(DEV_ADMIN_EMAIL) \
		MAILCUBBY_ADMIN_PASSWORD=$(DEV_ADMIN_PASSWORD) \
		MAILCUBBY_DATA_DIR=./data-server \
		go run ./cmd/mailcubby serve & \
	else \
		MAILCUBBY_DATA_DIR=./data go run ./cmd/mailcubby serve & \
	fi; \
	go_pid=$$!; \
	pnpm --dir web dev & \
	web_pid=$$!; \
	trap 'kill $$go_pid $$web_pid 2>/dev/null || true' INT TERM EXIT; \
	wait

test: ## Run Go unit and integration tests
	go test ./...

test-race: ## Run Go tests with the race detector (requires CGO)
	CGO_ENABLED=1 go test -race ./...

test-web: ## Run frontend Vitest unit tests
	pnpm --dir web test

lint: ## Run golangci-lint, Biome check, and TypeScript typecheck
	@test -x ./bin/golangci-lint || { echo "bin/golangci-lint not found: run 'make tools' first" >&2; exit 1; }
	./bin/golangci-lint run
	pnpm --dir web lint
	pnpm --dir web typecheck

fmt: ## Format Go and frontend source files
	@test -x ./bin/golangci-lint || { echo "bin/golangci-lint not found: run 'make tools' first" >&2; exit 1; }
	./bin/golangci-lint fmt
	pnpm --dir web exec biome format --write .

check: ## Verify generated code is up to date, then lint, test, and build-all
	@set -eu; \
	hash_gen() { \
		if command -v sha256sum >/dev/null 2>&1; then hcmd="sha256sum"; else hcmd="shasum -a 256"; fi; \
		{ \
			if [ -d internal/store/sqlc ]; then find internal/store/sqlc -type f | sort; fi; \
			if [ -f web/src/api/schema.d.ts ]; then echo web/src/api/schema.d.ts; fi; \
		} | while IFS= read -r f; do $$hcmd "$$f"; done; \
	}; \
	hash_file() { \
		if command -v sha256sum >/dev/null 2>&1; then hcmd="sha256sum"; else hcmd="shasum -a 256"; fi; \
		if [ -f "$$1" ]; then $$hcmd "$$1"; fi; \
	}; \
	before=$$(hash_gen); \
	$(MAKE) generate; \
	after=$$(hash_gen); \
	if [ "$$before" != "$$after" ]; then \
		echo "check: generated files changed after 'make generate'; commit updated generated code" >&2; \
		exit 1; \
	fi; \
	rt_before=$$(hash_file web/src/routeTree.gen.ts); \
	$(MAKE) lint; \
	$(MAKE) test; \
	$(MAKE) test-web; \
	$(MAKE) build-all; \
	rt_after=$$(hash_file web/src/routeTree.gen.ts); \
	if [ "$$rt_before" != "$$rt_after" ]; then \
		echo "check: web/src/routeTree.gen.ts changed during build; commit updated routeTree.gen.ts" >&2; \
		exit 1; \
	fi; \
	pnpm --dir web check:bundle

bench: ## Run Go benchmarks (store, search, ingest, smtpd, app)
	@pkgs=""; \
	for p in ./internal/store ./internal/search ./internal/ingest ./internal/smtpd ./internal/app; do \
		if [ -d "$$p" ]; then pkgs="$$pkgs $$p"; fi; \
	done; \
	if [ -z "$$pkgs" ]; then \
		echo "bench: no benchmark packages exist yet (added in Phase 5)"; \
	else \
		go test -run='^$$' -bench=. -benchmem $$pkgs; \
	fi

e2e: ## Build binary and run Playwright end-to-end tests
	@if [ ! -f web/playwright.config.ts ]; then \
		echo "e2e: skipping Playwright tests (web/playwright.config.ts does not exist yet; added in Phase 4a)"; \
	else \
		$(MAKE) build-all; \
		pnpm --dir web exec playwright test; \
	fi

seed: ## Send testdata/mime/*.eml fixtures to a running instance (SMTP, USER, COUNT)
	@if [ ! -d testdata/mime ] || [ -z "$$(ls testdata/mime/*.eml 2>/dev/null)" ]; then \
		echo "seed: skipping (testdata/mime/*.eml does not exist yet; added in Phase 2)"; \
	else \
		test -x ./bin/mailcubby || $(MAKE) build; \
		i=1; \
		while [ "$$i" -le "$(COUNT)" ]; do \
			for f in testdata/mime/*.eml; do \
				./bin/mailcubby sendmail -t --smtp "$(SMTP)" --user "$(USER)" --password dev < "$$f"; \
			done; \
			i=$$((i + 1)); \
		done; \
	fi

docker: ## Build local Docker image mailcubby:dev
	docker build -f deploy/Dockerfile \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) \
		-t mailcubby:dev .

migration: ## Create next sequential goose SQL migration (usage: make migration name=add_x)
	@set -eu; \
	if [ -z "$(name)" ]; then \
		echo "usage: make migration name=<snake_case_name>" >&2; \
		exit 1; \
	fi; \
	mkdir -p migrations; \
	last=$$(find migrations -maxdepth 1 -name '[0-9][0-9][0-9][0-9][0-9]_*.sql' -exec basename {} \; | sort | tail -n 1); \
	if [ -z "$$last" ]; then \
		next=1; \
	else \
		num=$$(echo "$$last" | cut -d_ -f1 | sed 's/^0*//'); \
		next=$$((num + 1)); \
	fi; \
	file=$$(printf "migrations/%05d_%s.sql" "$$next" "$(name)"); \
	printf -- "-- +goose Up\n\n-- +goose Down\n" > "$$file"; \
	echo "Created $$file"

clean: ## Remove build artifacts (preserves internal/web/dist/.gitkeep)
	rm -rf bin web/dist
	@if [ -d internal/web/dist ]; then \
		find internal/web/dist -mindepth 1 -maxdepth 1 ! -name '.gitkeep' -exec rm -rf {} +; \
	fi

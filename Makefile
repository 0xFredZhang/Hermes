# Hermes — development Makefile.
# `make run` loads .env and starts the server locally.

# Load .env (if present) and export its keys so config.Load() sees them.
ifneq (,$(wildcard .env))
include .env
export
endif

BINARY := bin/hermes
PKG    := ./cmd/hermes

.DEFAULT_GOAL := help
.PHONY: run build css css-watch test vet fmt tidy gen-key env clean help setup-pulumi test-integration doctor reset-local reset-local-state check

css: ## Build the Tailwind console stylesheet
	npm run css:build

css-watch: ## Rebuild the Tailwind console stylesheet on template changes
	npm run css:watch

run: ## Load .env and start the server locally
	go run $(PKG)

build: css ## Compile the binary to bin/hermes
	go build -o $(BINARY) $(PKG)

test: ## Run all tests
	go test ./...

vet: ## Run go vet
	go vet ./...

doctor: ## Check local Pulumi prerequisites without calling AWS
	go run $(PKG) doctor

reset-local: ## Remove the local DB (requires CONFIRM=reset; stop Hermes first)
	@if [ "$${CONFIRM:-}" != "reset" ]; then \
		echo "refusing reset: set CONFIRM=reset exactly"; \
		exit 1; \
	fi
	go run $(PKG) reset-local --confirm

reset-local-state: ## Remove local Pulumi state (requires CONFIRM=reset-state; optional FORCE=1)
	@if [ "$${CONFIRM:-}" != "reset-state" ]; then \
		echo "refusing state reset: set CONFIRM=reset-state exactly"; \
		exit 1; \
	fi
	@if [ -n "$${FORCE:-}" ] && [ "$${FORCE:-}" != "1" ]; then \
		echo "invalid FORCE value: use FORCE=1 or leave it unset"; \
		exit 1; \
	fi
	go run $(PKG) reset-local-state --confirm $(if $(filter 1,$(FORCE)),--force,)

check: ## Run generated, JS, format, unit, vet, and build checks (no AWS)
	npm run css:build
	git diff --exit-code -- internal/web/static/app.css internal/web/static/tabler.min.js internal/web/static/fonts
	npm run js:test
	@unformatted="$$(gofmt -l cmd internal)"; \
		if [ -n "$$unformatted" ]; then \
			echo "gofmt required:"; \
			echo "$$unformatted"; \
			exit 1; \
		fi
	GOENV=off GOFLAGS=-tags= go test ./...
	GOENV=off GOFLAGS=-tags= go vet ./...
	GOENV=off GOFLAGS=-tags= go build ./...

fmt: ## Format the code
	go fmt ./...

tidy: ## Tidy go.mod dependencies
	go mod tidy

gen-key: ## Print a fresh base64-encoded 32-byte master key
	@head -c 32 /dev/urandom | base64

env: ## Create .env from .env.example (fills a random master key)
	@if [ -f .env ]; then \
		echo ".env already exists, not overwriting"; \
	else \
		sed 's|^HERMES_MASTER_KEY=.*|HERMES_MASTER_KEY='"$$(head -c 32 /dev/urandom | base64)"'|' .env.example > .env; \
		echo "wrote .env (random HERMES_MASTER_KEY) — remember to change HERMES_LOGIN_PASSWORD"; \
	fi

clean: ## Remove build artifacts
	rm -rf bin

setup-pulumi: ## Install Pulumi provider plugins (requires pulumi CLI on PATH)
	@command -v pulumi >/dev/null || { echo "pulumi CLI not found — install: https://www.pulumi.com/docs/install/"; exit 1; }
	pulumi plugin install resource aws
	pulumi plugin install resource random

test-integration: ## Run the real-AWS integration test (needs pulumi + AWS creds in env)
	go test -tags integration ./internal/provisioner/pulumiengine/ -run TestIntegration -v

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

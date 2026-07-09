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
.PHONY: run build css css-watch test vet fmt tidy gen-key env clean help setup-pulumi test-integration

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
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-9s\033[0m %s\n", $$1, $$2}'

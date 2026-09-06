GO      ?= go
BIN     := bin
PKGS    := ./...

.PHONY: all build test race vet fmt cover census metrics py-test py-lint clean help

all: fmt vet test build py-lint py-test ## Everything CI runs

build: ## Build all binaries into bin/
	@mkdir -p $(BIN)
	$(GO) build -o $(BIN)/ $(PKGS)

test: ## Run tests
	$(GO) test $(PKGS)

race: ## Run tests with the race detector (this is what CI runs)
	$(GO) test -race $(PKGS)

vet: ## Run go vet
	$(GO) vet $(PKGS)

fmt: ## Format all Go source
	$(GO) fmt $(PKGS)

cover: ## Run tests with coverage and write coverage.html
	$(GO) test -coverprofile=coverage.out $(PKGS)
	$(GO) tool cover -html=coverage.out -o coverage.html
	@$(GO) tool cover -func=coverage.out | tail -1

census: ## Ingest the full pool census from live mainnet Horizon
	$(GO) run ./cmd/ingest -out data/runs

metrics: ## Print the metrics table for a run (make metrics RUN=data/runs/...)
	@test -n "$(RUN)" || { echo "usage: make metrics RUN=data/runs/<run>"; exit 2; }
	cd pkg/metrics && python3 -m gauge_metrics $(CURDIR)/$(RUN)

py-test: ## Run the metrics layer test suite
	cd pkg/metrics && python3 -m pytest -q

py-lint: ## Lint and format-check the metrics layer
	cd pkg/metrics && python3 -m ruff check . && python3 -m ruff format --check .

clean: ## Remove build and coverage artifacts
	rm -rf $(BIN) coverage.out coverage.html

help: ## List available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'

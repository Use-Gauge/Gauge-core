GO      ?= go
BIN     := bin
PKGS    := ./...

.PHONY: all build test race vet fmt cover census clean help

all: fmt vet test build ## Format, vet, test and build

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

clean: ## Remove build and coverage artifacts
	rm -rf $(BIN) coverage.out coverage.html

help: ## List available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'

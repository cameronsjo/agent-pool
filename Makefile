## ─── Variables ────────────────────────────────────────────────────────────────

BINARY     := agent-pool
MODULE     := git.sjo.lol/cameron/agent-pool
BUILD_DIR  := bin
CMD_DIR    := cmd/agent-pool
GO         := go
GOFLAGS    :=
LDFLAGS    := -s -w

## ─── Default ─────────────────────────────────────────────────────────────────

.PHONY: help
## Show available targets with descriptions
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | head -1
	@echo ""
	@grep -B1 -E '^[a-zA-Z_-]+:' $(MAKEFILE_LIST) | grep -E '^##|^[a-zA-Z_-]+:' | \
		sed 'N;s/\n/\t/' | sed 's/## //' | column -t -s '	'

## ─── Development ─────────────────────────────────────────────────────────────

.PHONY: build
## Build the agent-pool binary
build:
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./$(CMD_DIR)

.PHONY: run
## Build and run the daemon (pass POOL=<path> to specify pool dir)
run: build
	$(BUILD_DIR)/$(BINARY) start $(POOL)

.PHONY: dev
## Run with go run (no binary, faster iteration)
dev:
	$(GO) run ./$(CMD_DIR) start $(POOL)

COVERAGE_THRESHOLD ?= 65

## ─── Testing ─────────────────────────────────────────────────────────────────

.PHONY: test
## Run all tests
test:
	$(GO) test ./... -v

.PHONY: test-cover
## Run tests with coverage report (HTML)
test-cover:
	$(GO) test -coverprofile=coverage.out -covermode=atomic -coverpkg=./... ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

.PHONY: test-gaps
## Show functions below coverage threshold (default 70%, override with THRESHOLD=N)
test-gaps:
	@scripts/coverage-gaps.sh $(THRESHOLD)

.PHONY: test-gates
## Fail if total coverage drops below threshold (CI gate)
test-gates:
	$(GO) test -coverprofile=coverage.out -covermode=atomic -coverpkg=./... ./...
	@COVERAGE=$$($(GO) tool cover -func=coverage.out | grep ^total: | awk '{print $$NF}' | tr -d '%'); \
	echo "Total coverage: $${COVERAGE}%  (threshold: $(COVERAGE_THRESHOLD)%)"; \
	awk "BEGIN {exit ($${COVERAGE} < $(COVERAGE_THRESHOLD))}"

## ─── Quality ─────────────────────────────────────────────────────────────────

.PHONY: lint
## Run linter
lint:
	golangci-lint run ./...

.PHONY: fmt
## Format all Go files
fmt:
	$(GO) fmt ./...
	goimports -w .

.PHONY: vet
## Run go vet
vet:
	$(GO) vet ./...

.PHONY: check
## Run all quality checks (vet + lint + test)
check: vet lint test

## ─── Dependencies ────────────────────────────────────────────────────────────

.PHONY: deps
## Download and tidy dependencies
deps:
	$(GO) mod download
	$(GO) mod tidy

## ─── Clean ───────────────────────────────────────────────────────────────────

.PHONY: clean
## Remove build artifacts and coverage files
clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html

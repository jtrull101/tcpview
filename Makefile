BINARY     := tcpview
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    := -s -w -X main.version=$(VERSION)
FUZZTIME   := 10s
COVER_MIN  := 90

.PHONY: all
all: build ## Build the tcpview binary (default target)

.PHONY: build
build: ## Build the tcpview binary
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) .

.PHONY: run
run: build ## Build and run tcpview (needs CAP_NET_RAW - see setcap/run-sudo)
	./$(BINARY)

.PHONY: run-sudo
run-sudo: build ## Build and run tcpview via sudo (no setcap needed)
	sudo ./$(BINARY)

.PHONY: setcap
setcap: build ## Grant the built binary CAP_NET_RAW/CAP_NET_ADMIN so it can run without sudo
	sudo setcap cap_net_raw,cap_net_admin=eip ./$(BINARY)

.PHONY: install
install: ## go install tcpview into GOPATH/bin
	go install -trimpath -ldflags "$(LDFLAGS)" .

.PHONY: test
test: ## Run the test suite
	go test ./...

.PHONY: test-race
test-race: ## Run the test suite with the race detector
	go test ./... -race

.PHONY: cover
cover: ## Run tests with coverage and print the per-function + aggregate summary
	go test ./... -race -covermode=atomic -coverprofile=coverage.out
	go tool cover -func=coverage.out

.PHONY: cover-html
cover-html: cover ## Generate and open an HTML coverage report
	go tool cover -html=coverage.out -o coverage.html
	@echo "coverage.html written"

.PHONY: cover-check
cover-check: cover ## Run cover, then fail if aggregate coverage is below $(COVER_MIN)%
	@total=$$(go tool cover -func=coverage.out | tail -1 | awk '{print $$3}' | tr -d '%'); \
	echo "Aggregate line coverage: $${total}%"; \
	awk -v c="$$total" -v min="$(COVER_MIN)" 'BEGIN { exit (c + 0 >= min) ? 0 : 1 }' \
		|| { echo "Aggregate coverage $${total}% is below the $(COVER_MIN)% gate"; exit 1; }

.PHONY: fuzz
fuzz: ## Run a short fuzz smoke pass (FUZZTIME=10s) across every Fuzz target
	go test ./internal/filter/... -run=^$$ -fuzz=FuzzParseExpression -fuzztime=$(FUZZTIME)
	go test ./internal/filter/... -run=^$$ -fuzz=FuzzFilterMatches -fuzztime=$(FUZZTIME)
	go test ./internal/capture/... -run=^$$ -fuzz=FuzzDecode -fuzztime=$(FUZZTIME)
	go test ./internal/store/... -run=^$$ -fuzz=FuzzReadArchiveFile -fuzztime=$(FUZZTIME)
	go test ./internal/remote/... -run=^$$ -fuzz=FuzzParseTcpdumpList -fuzztime=$(FUZZTIME)
	go test . -run=^$$ -fuzz=FuzzParseCapEff -fuzztime=$(FUZZTIME)

.PHONY: fmt
fmt: ## Reformat the codebase with gofmt
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file is not gofmt-formatted
	@fmt_out="$$(gofmt -l .)"; \
	if [ -n "$$fmt_out" ]; then \
		echo "Not gofmt-formatted:"; echo "$$fmt_out"; exit 1; \
	fi

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint (must be installed: https://golangci-lint.run)
	golangci-lint run ./...

.PHONY: check
check: fmt-check vet lint test-race cover-check ## Run everything CI runs (fmt, vet, lint, race tests, coverage gate)

.PHONY: tidy
tidy: ## Tidy go.mod/go.sum
	go mod tidy

.PHONY: clean
clean: ## Remove build/test/coverage artifacts
	rm -f $(BINARY) coverage.out coverage.html
	rm -rf dist pkg

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

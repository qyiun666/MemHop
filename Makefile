.PHONY: build build-mcp test test-e2e test-integration test-unit test-mcp test-affected bench lint fmt clean help doctor

# --- Prerequisites -------------------------------------------------------
# `make test-unit` is fully offline: the api/internal suites plus the mock-backed
# files under test/** need no network at all.
#
# Only seven test/ files carry the `integration` build tag and require a real
# endpoint (that is `make test-e2e`): benchmark / core_cycle / e2e_flow /
# fidelity / fixture / keyword_extraction_e2e / open. Credentials come from
# MEMHOP_TEST_LLM_KEY / MEMHOP_TEST_LLM_URL / MEMHOP_TEST_LLM_MODEL (or
# test/testsupport/key_config.json). There is no embedding model to pull — the
# engine contacts none.

# ---- Targets -----------------------------------------------------------

## build the public SDK (main library only, no test packages)
build:
	go build ./...

## offline gate — public facade, internal white-box, and the mock-backed
## interface suite under test/**
test-unit:
	go test -race ./api/... ./internal/... ./test/...

## build the MCP server binary
build-mcp:
	go build -o bin/memhop-mcp ./cmd/memhop-mcp

## MCP server unit + SSE smoke tests (offline, no Ollama/LLM needed)
test-mcp:
	go test ./cmd/memhop-mcp/...

## interface tests — external black-box tests under test/**
## Requires Ollama daemon + the embedding model + LLM credentials.
test-e2e:
	go test ./test/... -tags=integration -count=1 -v -timeout=10m

test-integration:
	go test ./test/... -tags=integration -count=1 -v -timeout=30m

## run tests for Go packages changed since HEAD
test-affected:
	@changed=$$(git diff --name-only HEAD | grep '\.go$$' | xargs -I{} dirname {} | sort -u | sed 's|^|./|'); \
	if [ -z "$$changed" ]; then echo "No Go files changed."; exit 0; fi; \
	echo "Testing: $$changed"; \
	go test $$changed

## run everything (unit + interface)
test: test-unit test-integration

## benchmarks (interface + baseline-comparison, requires Ollama)
bench:
	go test -tags=integration -bench=. -benchmem -run=^$$ ./test/...

lint:
	go vet ./...

fmt:
	gofmt -w api internal cmd test

clean:
	rm -rf bin/ vendor/

## check development environment (Go version)
doctor:
	@echo "=== MemHop Development Environment Check ==="
	@echo ""
	@echo "[1/1] Go version..."
	@go version
	@echo ""
	@echo "=== All checks complete ==="

help:
	@echo "Targets:"
	@echo "  build             build the memhop SDK library"
	@echo "  build-mcp         build the MCP server binary"
	@echo "  test              run all tests (unit + interface)"
	@echo "  test-affected     run tests for Go packages changed since HEAD"
	@echo "  test-unit         run only internal unit tests"
	@echo "  test-mcp          run MCP offline tests (no Ollama/LLM needed)"
	@echo "  test-e2e          run integration tests (needs Ollama + LLM)"
	@echo "  test-integration  run integration tests (needs Ollama + LLM)"
	@echo "  bench             run benchmarks (needs Ollama)"
	@echo "  lint              go vet across all packages"
	@echo "  fmt               gofmt -w api internal test"
	@echo "  clean             remove build artefacts"
	@echo "  doctor            check development environment (Go version)"

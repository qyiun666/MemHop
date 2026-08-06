.PHONY: build test test-e2e test-integration test-unit test-affected bench lint fmt clean help doctor

# --- Prerequisites for interface tests -----------------------------------
# 1) Ollama daemon:      `ollama serve`
# 2) Embedding model:    `ollama pull nomic-embed-text`  (768d, default)
# Env overrides consumed by test/testsupport:
#   OLLAMA_HOST                 default http://127.0.0.1:11434
#   MEMHOP_TEST_EMBED_MODEL     default nomic-embed-text
#   MEMHOP_TEST_VECTOR_DIM      default 768
#
# When Ollama is unreachable the interface suite skips (exit 0), not fails.

# ---- Targets -----------------------------------------------------------

## build the public SDK (main library only, no test packages)
build:
	go build ./api/... ./internal/...

## unit tests — internal white-box tests (api + internal)
test-unit:
	go test -race ./api/... ./internal/...

## interface tests — external black-box tests under test/**
## Requires Ollama daemon + the embedding model to be available.
test-e2e:
	go test ./test/... -count=1 -v -timeout=10m

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
	go test -bench=. -benchmem -run=^$$ ./test/...

lint:
	go vet ./api/... ./internal/... ./test/...

fmt:
	gofmt -w api internal test

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
	@echo "  test              run all tests (unit + interface)"
	@echo "  test-affected     run tests for Go packages changed since HEAD"
	@echo "  test-unit         run only internal unit tests"
	@echo "  test-e2e          run e2e tests (no build tag, needs Ollama)"
	@echo "  test-integration  run only external interface tests (needs Ollama)"
	@echo "  bench             run benchmarks (needs Ollama)"
	@echo "  lint              go vet across api/, internal/ and test/"
	@echo "  fmt               gofmt -w memhop test"
	@echo "  clean             remove build artefacts"
	@echo "  doctor            check development environment (Go version)"

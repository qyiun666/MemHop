.PHONY: build test test-integration test-unit bench lint fmt clean help

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
	go test ./api/... ./internal/...

## interface tests — external black-box tests under test/**
## Requires Ollama daemon + the embedding model to be available.
test-integration:
	go test ./test/...

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

help:
	@echo "Targets:"
	@echo "  build             build the memhop SDK library"
	@echo "  test              run all tests (unit + interface)"
	@echo "  test-unit         run only internal unit tests"
	@echo "  test-integration  run only external interface tests (needs Ollama)"
	@echo "  bench             run benchmarks (needs Ollama)"
	@echo "  lint              go vet across api/, internal/ and test/"
	@echo "  fmt               gofmt -w memhop test"
	@echo "  clean             remove build artefacts"

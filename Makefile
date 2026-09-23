.PHONY: build test test-e2e test-integration test-unit test-affected bench lint check-guides fmt clean help doctor

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

## compile the runnable skeleton both integration guides embed in §11 — a host that
## copies the example should not meet a compile error. Fully offline: the extracted
## programs build in a throwaway module whose MemHop requirement is replaced by this
## checkout, and GOPROXY stays off so nothing reaches the network.
check-guides:
	@dir=$$(mktemp -d); trap 'rm -rf $$dir' EXIT; \
	printf 'module memhop.guidecheck\n\ngo 1.27\n\nrequire github.com/qyiun666/MemHop v0.0.0\n\nreplace github.com/qyiun666/MemHop => %s\n' "$$(pwd)" > $$dir/go.mod; \
	fail=0; i=0; \
	for guide in INTEGRATION_GUIDE.md INTEGRATION_GUIDE.zh.md; do \
	  i=$$((i+1)); mkdir -p $$dir/skel$$i; \
	  awk '/^```go$$/{inb=1; buf=""; next} /^```$$/{if (inb && buf ~ /^package /) printf "%s", buf; inb=0; next} inb{buf = buf $$0 "\n"}' \
	    $$guide > $$dir/skel$$i/main.go; \
	  test -s $$dir/skel$$i/main.go || { echo "$$guide: no package-main skeleton"; fail=1; }; \
	done; \
	GOFLAGS=-mod=mod GOPROXY=off go build -C $$dir ./... || fail=1; \
	test $$fail -eq 0 && echo "guide skeletons build (2 guides)"; \
	exit $$fail

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
	@echo "  test-e2e          run integration tests (needs Ollama + LLM)"
	@echo "  test-integration  run integration tests (needs Ollama + LLM)"
	@echo "  bench             run benchmarks (needs Ollama)"
	@echo "  lint              go vet across all packages"
	@echo "  check-guides      compile the runnable skeleton embedded in both integration guides"
	@echo "  fmt               gofmt -w api internal test"
	@echo "  clean             remove build artefacts"
	@echo "  doctor            check development environment (Go version)"

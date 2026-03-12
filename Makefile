lint:
	golangci-lint run --fix

lint-ci:
	golangci-lint run --timeout 10m

fmt:
	gofmt -w -s .
	goimports -w .

test:
	go test -race .
	cd benchmark && go test -race .

test-coverage:
	go test -race -coverprofile=coverage.out .
	go tool cover -html=coverage.out -o coverage.html

bench:
	cd benchmark && go test -bench=. -benchmem .
	go test -run=^$ -bench=. -benchmem .

# Run benchmarks (count=5) and save baseline (root + ./benchmark)
bench-baseline:
	@mkdir -p .benchdata
	cd benchmark && go test -run=^$ -bench=Velox -benchmem -count=5 . >> ../.benchdata/baseline.txt 2>/dev/null || true
	@echo "Baseline saved to .benchdata/baseline.txt"

# Check for regressions against baseline (default threshold: 10%)
bench-check:
	@./scripts/benchcheck.sh

# Check with custom threshold: make bench-check-threshold THRESHOLD=5
bench-check-threshold:
	@./scripts/benchcheck.sh .benchdata/baseline.txt $(or $(THRESHOLD),10)

clean:
	go clean
	rm -f coverage.out coverage.html cpu.out mem.out

FUZZ_TIME ?= 30s

fuzz:
	go test -fuzz=FuzzMarshalString -fuzztime=$(FUZZ_TIME) .
	go test -fuzz=FuzzMarshalStruct -fuzztime=$(FUZZ_TIME) .
	go test -fuzz=FuzzMarshalNoCrash -fuzztime=$(FUZZ_TIME) .
	go test -fuzz=FuzzUnmarshalAny -fuzztime=$(FUZZ_TIME) .
	go test -fuzz=FuzzUnmarshalStruct -fuzztime=$(FUZZ_TIME) .
	go test -fuzz=FuzzUnmarshalNested -fuzztime=$(FUZZ_TIME) .
	go test -fuzz=FuzzNoCrash -fuzztime=$(FUZZ_TIME) .

# Detect host platform
_HOST_OS := $(shell uname -s | tr '[:upper:]' '[:lower:]')
_HOST_ARCH := $(shell uname -m)

# Normalize arch
ifeq ($(_HOST_ARCH),x86_64)
  _HOST_ARCH := amd64
else ifeq ($(_HOST_ARCH),aarch64)
  _HOST_ARCH := arm64
endif

# Default to host platform
TARGET_OS ?= $(_HOST_OS)
TARGET_ARCH ?= $(_HOST_ARCH)

gen:
	@SOURCE_FILE="$(CURDIR)/native/encvm/impl/encvm.c" \
	 TARGET_DIR="$(CURDIR)/native/encvm" \
	 STDLIB_SOURCES="$(CURDIR)/native/stdlib/memory.c" \
	 bash scripts/gen-natives.sh $(if $(USE_ZIG),--zig) "$(TARGET_OS)" "$(TARGET_ARCH)"

# Generate benchmark visualization SVG
# Usage: make benchviz
#        make benchviz BENCH_FILTER="Benchmark_Marshal.*" BENCH_TITLE="Marshal Performance"
BENCH_FILTER ?= .
BENCH_TITLE ?= Benchmark Results
BENCH_COUNT ?= 3
BENCH_OUTPUT ?= local/benchmark.svg

benchviz:
	@mkdir -p $(dir $(BENCH_OUTPUT))
	cd benchmark && go test -run='^$$' -bench='$(BENCH_FILTER)' -benchmem -count=$(BENCH_COUNT) . \
	  | go run ./benchviz/ -title '$(BENCH_TITLE)' > '../$(BENCH_OUTPUT)'
	@echo "SVG saved to $(BENCH_OUTPUT)"

.PHONY: lint lint-ci fmt test test-coverage bench bench-baseline bench-check bench-check-threshold clean fuzz gen benchviz

lint:
	golangci-lint run --fix

lint-ci:
	golangci-lint run --timeout 10m

fmt:
	gofmt -w -s .
	goimports -w .

test:
	@./scripts/run-test.sh

test-coverage:
	go test -race -coverprofile=coverage.out .
	go tool cover -html=coverage.out -o coverage.html

clean:
	go clean
	rm -f coverage.out coverage.html cpu.out mem.out

FUZZ_TIME ?= 30s
FUZZ_PARALLEL ?= 4
FUZZ_TARGETS := FuzzMarshalString FuzzMarshalStruct FuzzMarshalNoCrash \
                FuzzUnmarshalAny FuzzUnmarshalStruct FuzzUnmarshalNested FuzzNoCrash

fuzz:
	@for t in $(FUZZ_TARGETS); do \
		go test -fuzz=$$t -fuzztime=$(FUZZ_TIME) . || exit 1; \
	done

fuzz-parallel:
	@for t in $(FUZZ_TARGETS); do \
		go test -fuzz=$$t -parallel=$(FUZZ_PARALLEL) -fuzztime=$(FUZZ_TIME) . || exit 1; \
	done

fuzz-concurrent:
	@echo "Running $(words $(FUZZ_TARGETS)) fuzz targets concurrently..."
	@$(foreach t,$(FUZZ_TARGETS),go test -fuzz=$(t) -parallel=$(FUZZ_PARALLEL) -fuzztime=$(FUZZ_TIME) . &) wait
	@echo "All fuzz tests completed"

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
# Set NO_PRELINK=1 to disable prelink path in scripts/gen-natives.sh
GEN_NATIVE_PRELINK_FLAG := $(if $(NO_PRELINK),--no-prelink,)

# Auto-detect cross-compilation: use zig when target differs from host
_IS_CROSS := $(if $(or \
  $(filter-out $(_HOST_OS),$(TARGET_OS)), \
  $(filter-out $(_HOST_ARCH),$(TARGET_ARCH))),1,)
_AUTO_ZIG := $(or $(USE_ZIG),$(_IS_CROSS))

gen:
	@bash scripts/gen-natives.sh $(if $(_AUTO_ZIG),--zig) $(if $(ASM),--asm) $(GEN_NATIVE_PRELINK_FLAG) \
		native/encvm/sources.sh "$(TARGET_OS)" "$(TARGET_ARCH)"

# Generate native artifacts optimized using AutoFDO sample profile data.
# Requires local/pgo-data/merged.profdata (generated via perf + create_llvm_prof).
gen-with-pgo:
	@bash scripts/gen-natives.sh $(if $(_AUTO_ZIG),--zig) $(if $(ASM),--asm) $(GEN_NATIVE_PRELINK_FLAG) --pgo-use \
		native/encvm/sources.sh "$(TARGET_OS)" "$(TARGET_ARCH)"

# Generate native artifacts for debugging:
# - enable encvm trace (VJ_ENCVM_DEBUG)
# - keep richer syso symbols for native debugging
# Use with: go test -tags vjdebug -run TestFoo -v
gen-debug:
	@EXTRA_CFLAGS="-DVJ_ENCVM_DEBUG" DEBUG_SYMBOLS=1 bash scripts/gen-natives.sh $(if $(_AUTO_ZIG),--zig) $(if $(ASM),--asm) $(GEN_NATIVE_PRELINK_FLAG) \
		native/encvm/sources.sh "$(TARGET_OS)" "$(TARGET_ARCH)"

# Decode VM (Rust) native build
gen-rsdec:
	@bash scripts/gen-rsdec.sh "$(TARGET_OS)" "$(TARGET_ARCH)"

gen-rsdec-debug:
	@bash scripts/gen-rsdec.sh --debug "$(TARGET_OS)" "$(TARGET_ARCH)"

BENCH_FILTER ?= .
BENCH_TITLE ?= Benchmark Results
BENCH_COUNT ?= 5
BENCH_OUTPUT ?= local/benchmark.txt
BENCH_LIBS ?=
BENCH_TIME ?= 3s
GOOS ?= $(_HOST_OS)
GOARCH ?= $(_HOST_ARCH)
BENCH_BIN ?= local/bin/vjson-benchmark_$(GOOS)_$(GOARCH)

bench-build:
	mkdir -p $(dir $(BENCH_BIN))
	cd benchmark && GOOS=$(GOOS) GOARCH=$(GOARCH) go test -c -o ../$(BENCH_BIN) .

benchviz: bench-build
	mkdir -p $(dir $(BENCH_OUTPUT));
	bash scripts/bench.sh -b $(BENCH_BIN) -f '$(BENCH_FILTER)' -t $(BENCH_TIME) -c $(BENCH_COUNT) -w $(if $(BENCH_LIBS),-l $(BENCH_LIBS)) -o '$(BENCH_OUTPUT)';
	(cd benchmark && go run ./benchviz/ -title '$(BENCH_TITLE)' -format html < '../$(BENCH_OUTPUT)' > '../$(basename $(BENCH_OUTPUT)).html');
	(cd benchmark && go run ./benchviz/ -title '$(BENCH_TITLE)' -format svg < '../$(BENCH_OUTPUT)' > '../$(basename $(BENCH_OUTPUT)).svg');

# Compare libraries with benchstat
# Usage: make benchcmp BENCH_CMP="Velox StdJSON"
#        make benchcmp BENCH_CMP="Velox Sonic StdJSON" BENCH_FILTER=Marshal
BENCH_CMP ?= Velox StdJSON
BENCHCMP_OUTPUT ?= local/benchcmp.txt

benchcmp: bench-build
	bash scripts/benchcmp.sh -b $(BENCH_BIN) -f '$(BENCH_FILTER)' -c $(BENCH_COUNT) -w -o '$(BENCHCMP_OUTPUT)' $(BENCH_CMP)

# Package benchmark binary + scripts for remote testing
# Usage: make bench-pack GOOS=linux GOARCH=amd64
BENCH_PACK ?= local/vjson-bench_$(GOOS)_$(GOARCH).tar.gz

bench-pack: bench-build
	tar czf $(BENCH_PACK) -C $(CURDIR) Makefile scripts/bench.sh scripts/benchcmp.sh scripts/bench-run.sh $(BENCH_BIN)
	@echo "Packed: $(BENCH_PACK)"

.PHONY: lint lint-ci fmt test test-coverage bclean fuzz fuzz-parallel fuzz-concurrent gen gen-debug gen-pgo-use gen-rsdec gen-rsdec-debug bench-build bench-pack benchviz benchcmp

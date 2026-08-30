lint:
	golangci-lint run --fix

lint-ci:
	golangci-lint run --timeout 10m

hooks:
	git config core.hooksPath githooks

fmt:
	gofmt -w -s .
	goimports -w .
	clang-format -i native/encvm/impl/*.h native/encvm/impl/*.c
	clang-format -i native/ndec/impl/ndec/core/*.h native/ndec/impl/ndec/*.h

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
# (forwarded to module Makefiles; native/common.mk computes the flag).

# Toolchain selection: llvm (the only supported toolchain).
#   llvm = clang/lld-link (requires LINUX_SYSROOT for Linux targets)
# The project depends on LLVM clang >= 22; zig cc is no longer supported
# (it ships clang 20 and its linker rejects --fatal-warnings).
TOOLCHAIN ?= llvm

# Linux musl sysroot path (used when targeting Linux)
# Default: /opt/linux/sysroot if it exists, otherwise empty
LINUX_SYSROOT ?= $(shell test -d /opt/linux/sysroot && echo /opt/linux/sysroot || echo "")

# Native modules and the active module selector. gen/gen-debug/gen-pgo-instr-
# use/stack-check/pgo-instr-collect delegate to native/$(MODULE)/Makefile.
# MODULE has no default: bare `make gen` errors with a hint. Use the module
# shortcuts (make encvm/ndec/vlib) or pass MODULE=<module> explicitly.
NATIVE_LIBS := encvm ndec vlib
MODULE ?=

# stackdepth is shared across modules; built once at the top level.
STACKDEPTH := $(CURDIR)/build/bin/stackdepth

# Primitives forwarded to every module sub-make. Derived values
# (_ISA, _EXT, GEN_NATIVE_PRELINK_FLAG) are computed by native/common.mk from
# these, so they recompute correctly when a sub-make overrides TARGET_OS/ARCH
# (e.g. gen-all-platforms). MODE/STACK_BUDGET are forwarded only when set, so
# an unset value does not clobber the module's ?= default.
_MODULE_VARS = REPO_ROOT=$(CURDIR) TARGET_OS=$(TARGET_OS) TARGET_ARCH=$(TARGET_ARCH) \
	TOOLCHAIN=$(TOOLCHAIN) LINUX_SYSROOT=$(LINUX_SYSROOT) PROFILE=$(PROFILE) \
	ASM=$(ASM) NO_PRELINK=$(NO_PRELINK) NO_OPT=$(NO_OPT) \
	$(if $(MODE),MODE=$(MODE)) $(if $(STACK_BUDGET),STACK_BUDGET=$(STACK_BUDGET))

$(STACKDEPTH):
	@cd scripts/cmd/stackdepth && go build -o $@ .

# Shell guard inlined into each module-picking recipe. Errors naming the real
# target (passed as $1) with a matching example, instead of an internal helper
# target that hides which command the user ran.
_require_module = if [ -z "$(MODULE)" ]; then \
	echo "$1: MODULE is required (one of: $(NATIVE_LIBS))." >&2; \
	echo "  e.g. make $1 MODULE=encvm" >&2; \
	exit 1; \
	fi

gen:
	@$(call _require_module,gen); $(MAKE) -C native/$(MODULE) gen $(_MODULE_VARS)

# Generate native artifacts for debugging:
# - enable trace (VJ_DEBUG)
# - keep richer syso symbols for native debugging
# Use with: go test -tags vjdebug -run TestFoo -v
gen-debug:
	@$(call _require_module,gen-debug); $(MAKE) -C native/$(MODULE) gen-debug $(_MODULE_VARS)

# Rebuild native artifacts with instrumentation PGO (exact block counts).
# Requires .local/pgo-data/instr.profdata (produce it with `make pgo-instr-collect`).
# MODULE selects which native module to rebuild (encvm/ndec/vlib).
gen-pgo-instr-use:
	@$(call _require_module,gen-pgo-instr-use); $(MAKE) -C native/$(MODULE) gen-pgo-instr-use $(_MODULE_VARS)
	@echo ""
	@echo "PGO $(MODULE) syso installed. To restore the committed version:"
	@echo "  git checkout -- native/$(MODULE)/*.syso"

# End-to-end instrumentation PGO collection: build an instrumented syso, run the
# workload (counters flushed via the vjpgoinstr TestMain hook), merge the
# profile, then rebuild the production syso with --pgo-instr-use. Records EXACT
# per-block counts (no perf needed), so it does not misjudge cold-but-important
# paths the way sampling can. Tunable via PGO_* env vars (see script header).
# Usage: make pgo-instr-collect MODULE=encvm          # encvm fast VM (Marshal workload)
#        make pgo-instr-collect MODULE=ndec           # ndec (decode workload)
#        make pgo-instr-collect MODULE=encvm MODES=full    # full VM (MarshalIndent workload)
#        make pgo-instr-collect MODULE=encvm MODES=compact # compact VM (escape workload)
#        make pgo-instr-collect MODULE=encvm MODES='full compact' # sequential multi-mode
#        make pgo-instr-collect MODULE=encvm TARGET_OS=linux TARGET_ARCH=amd64 MODES=fast
pgo-instr-collect:
	@$(call _require_module,pgo-instr-collect); MODULE="$(MODULE)" MODES="$(MODES)" \
	  LINUX_SYSROOT="$(LINUX_SYSROOT)" TOOLCHAIN="$(TOOLCHAIN)" \
	  bash scripts/pgo-collect-instr.sh "$(TARGET_OS)" "$(TARGET_ARCH)"

# gen-all: build every native module across every supported platform.
# Each module's Makefile declares its SUPPORTED_PLATFORMS and owns the
# platform sweep via its gen-all-platforms target.
gen-all:
	@for m in $(NATIVE_LIBS); do \
		echo "=== $$m: all platforms ==="; \
		$(MAKE) -C native/$$m gen-all-platforms $(_MODULE_VARS) || exit 1; \
	done

# Per-module shortcuts, auto-generated so the set stays symmetric without
# per-module boilerplate. <module> and <module>-debug come from NATIVE_LIBS;
# <module>-pgo only from PGO_LIBS (vlib excluded: its syso exports are
# init-time, the hot path is inline in callers, PGO would optimize nothing).
PGO_LIBS := encvm ndec

define _module_alias
.PHONY: $(1) $(1)-debug $(1)-lldb
$(1):
	@$(MAKE) gen MODULE=$(1)
$(1)-debug:
	@$(MAKE) gen-debug MODULE=$(1)
# <module>-lldb: build a syso for lldb source-level debugging of C code.
# Sets NO_PRELINK=1 (darwin: archive .syso with per-.o DWARF + relocations;
# linux: ld -r relocatable object) and NO_OPT=1 (-O0 so frame variable / step
# work as expected; auto-disables LTO and the stack-frame size check).
# See docs/debug-native.md for the full external-linking + lldb attach recipe.
$(1)-lldb:
	@$(MAKE) gen-debug MODULE=$(1) NO_PRELINK=1 NO_OPT=1
	@echo ''
	@echo 'syso built. Next steps (see docs/debug-native.md for full recipe):'
	@echo '  CGO_ENABLED=1 SDKROOT=/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk \'
	@echo '    PATH=/opt/llvm/current/bin:$$$$PATH \'
	@echo '    go build -ldflags='"'"'-linkmode=external -compressdwarf=false'"'"' -o /tmp/vj-debug .'
	@echo '  /tmp/vj-debug &'
	@echo '  PATH=/opt/llvm/current/bin:$$$$PATH lldb -p $$$$!'
endef
$(foreach m,$(NATIVE_LIBS),$(eval $(call _module_alias,$(m))))

define _module_pgo_alias
.PHONY: $(1)-pgo
$(1)-pgo:
	@$(MAKE) gen-pgo-instr-use MODULE=$(1)
endef
$(foreach m,$(PGO_LIBS),$(eval $(call _module_pgo_alias,$(m))))

ndec-test:
	make -C native/ndec test
	go test ./vbind ./decode/bind/ -count=1
	go test ./tests/ -count=1

# stack-check verifies that no nosplit native call chain from the NOSPLIT
# trampoline entry points exceeds Go's abi.StackNosplitBase (800B). The
# trampolines are NOSPLIT $0 tail calls, so the whole .syso call chain is
# invisible to the Go linker's own stack check and must be validated here.
# See scripts/cmd/stackdepth. Pass MODULE=encvm|ndec|vlib (no default).
# stack-check-all walks every module and, for encvm, every mode (full/compact/fast).
stack-check: $(STACKDEPTH)
	@$(call _require_module,stack-check); $(MAKE) -C native/$(MODULE) stack-check $(_MODULE_VARS)

stack-check-all: $(STACKDEPTH)
	@for m in $(NATIVE_LIBS); do \
		echo "=== stack-check $$m ==="; \
		$(MAKE) -C native/$$m stack-check-all $(_MODULE_VARS) || exit 1; \
	done

BENCH_FILTER ?= .
BENCH_COUNT ?= 5
BENCH_LIBS ?=
BENCH_TIME ?= 3s
GOOS ?= $(_HOST_OS)
GOARCH ?= $(_HOST_ARCH)
BENCH_BIN ?= local/bin/vjson-benchmark_$(GOOS)_$(GOARCH)

# benchviz writes <suite>-<N>.txt/.svg per run, N being the next free index in
# BENCHVIZ_DIR. Set CPU_SLUG to tag the directory with the machine's CPU
# (e.g. CPU_SLUG=amd-epyc7k62 to match the committed docs layout).
CPU_SLUG ?=
BENCHVIZ_DIR ?= docs/benchmarks/$(GOOS)-$(GOARCH)$(if $(CPU_SLUG),-$(CPU_SLUG))
BENCHVIZ_SUITES ?= Unmarshal Marshal
BENCHVIZ_LIBS ?=
BENCHVIZ_TIME ?= 5s
BENCHVIZ_COUNT ?= 2
BENCHVIZ_EXCLUDE ?= Compact

bench-build:
	mkdir -p $(dir $(BENCH_BIN))
	cd benchmark && GOOS=$(GOOS) GOARCH=$(GOARCH) go test -c -o ../$(BENCH_BIN) .

benchviz: bench-build
	bash scripts/benchviz.sh -b $(BENCH_BIN) -d '$(BENCHVIZ_DIR)' -s '$(BENCHVIZ_SUITES)' \
		-t $(BENCHVIZ_TIME) -c $(BENCHVIZ_COUNT) -x '$(BENCHVIZ_EXCLUDE)' \
		$(if $(BENCHVIZ_LIBS),-l '$(BENCHVIZ_LIBS)');

# Compare libraries with benchstat
# Usage: make benchcmp BENCH_CMP="Velox Sonic"
#        make benchcmp BENCH_CMP="Velox Sonic GoJSON JSONv2" BENCH_FILTER=Marshal
BENCH_CMP ?= Velox Sonic GoJSON JSONv2
BENCHCMP_OUTPUT ?= local/benchcmp.txt

benchcmp: bench-build
	bash scripts/benchcmp.sh -b $(BENCH_BIN) -f '$(BENCH_FILTER)' -c $(BENCH_COUNT) -w -o '$(BENCHCMP_OUTPUT)' $(BENCH_CMP)

# Package benchmark binary + scripts for remote testing
# Usage: make bench-pack GOOS=linux GOARCH=amd64
BENCH_PACK ?= local/vjson-bench_$(GOOS)_$(GOARCH).tar.gz

bench-pack: bench-build
	tar czf $(BENCH_PACK) -C $(CURDIR) Makefile scripts/bench.sh scripts/benchcmp.sh scripts/bench-run.sh $(BENCH_BIN)
	@echo "Packed: $(BENCH_PACK)"

# Profile the native C (syso) portion of a benchmark with perf, and resolve hot
# instruction addresses back to C source lines. Rebuilds the given native
# module(s) with PROFILE=1 (production -O3 -DNDEBUG + LTO codegen, but with -g3
# and a base-0 debug .so kept under build/prelink/), records with perf, then
# maps hot addresses to source via addr2line. See scripts/perf-native.sh -h.
#
# Usage:
#   make perf                                   # ndec / KubePods Velox (default)
#   make perf PERF_MODULE=encvm PERF_BENCH='Benchmark_Marshal_KubePods_Velox$$'
#   make perf PERF_ARGS='-m ndec -m vlib -t 15s -p 2.0'
#
# NOTE: make's own command-line option parser sees PERF_ARGS first, so
# options like -t/-p/-g get intercepted by make (e.g. -t means touch). For
# those, call the script directly:
#   bash scripts/perf-native.sh -m ndec -b 'Bench$' -t 8s -g 100
# Only -m/-b/-t have dedicated make variables (PERF_MODULE/PERF_BENCH/PERF_TIME).
PERF_MODULE ?=
PERF_BENCH  ?=
PERF_TIME   ?=
PERF_ARGS   ?=

perf:
	@bash scripts/perf-native.sh \
		$(if $(PERF_MODULE),-m $(PERF_MODULE)) \
		$(if $(PERF_BENCH),-b '$(PERF_BENCH)') \
		$(if $(PERF_TIME),-t $(PERF_TIME)) \
		$(PERF_ARGS)

# No top-level .PHONY list: none of these target names collide with files, so
# make treats them as always out-of-date. Module aliases self-declare .PHONY in
# their define templates. Add .PHONY for a target only if a same-named file or
# directory ever appears.

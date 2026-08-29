CC      ?= clang
CFLAGS  ?= -O2 -march=native

BUILD ?= build

INCLUDES = -I impl -I ../include

BENCH_CPU ?= 2
BENCH_TASKSET := $(if $(BENCH_CPU),$(if $(shell command -v taskset 2>/dev/null),taskset -c $(BENCH_CPU)))

CRITERION_INC    ?= /usr/local/lib/criterion/include
CRITERION_LIB    ?= /usr/local/lib/criterion/lib
CRITERION_LDFLAGS = -L$(CRITERION_LIB) -Wl,-rpath,$(CRITERION_LIB) -lcriterion

# zlib backs test/gzload.h. Override for a custom install.
ZLIB_INC ?=
ZLIB_LIB ?=
ifeq ($(ZLIB_LIB),)
ZLIB_LDFLAGS = -lz
else
ZLIB_LDFLAGS = -L$(ZLIB_LIB) -Wl,-rpath,$(ZLIB_LIB) -lz
endif
ifeq ($(ZLIB_INC),)
ZLIB_CFLAGS =
else
ZLIB_CFLAGS = -I$(ZLIB_INC)
endif

TEST_DEPS  = impl/ndec/core/*.h impl/ndec/*.h test/*.h
TEST_CFLAGS = -g -O0 -march=native $(INCLUDES) -I$(CRITERION_INC)
TEST_BINS  = $(BUILD)/sax_tests $(BUILD)/chunkfeed_tests $(BUILD)/dom_tests $(BUILD)/window_tests $(BUILD)/bind_tests

FILTER  ?=
VERBOSE ?= 2
TEST_FLAGS = $(if $(VERBOSE),--verbose=$(VERBOSE),) $(addprefix --filter=,$(FILTER))


.PHONY: test bench bench-build jsonfmt jsondata data debug bind-debug clean gen gen-debug gen-pgo-instr-use stack-check stack-check-all gen-all-platforms

test: $(TEST_BINS)
	$(BUILD)/sax_tests $(TEST_FLAGS)
	$(BUILD)/chunkfeed_tests $(TEST_FLAGS)
	$(BUILD)/dom_tests $(TEST_FLAGS)
	$(BUILD)/window_tests $(TEST_FLAGS)
	$(BUILD)/bind_tests $(TEST_FLAGS)

$(BUILD):
	mkdir -p $(BUILD)

# Pattern rule for chunkfeed/dom test binaries
$(BUILD)/%_tests: test/%_tests.c $(TEST_DEPS) | $(BUILD)
	$(CC) $(TEST_CFLAGS) -o $@ $< $(CRITERION_LDFLAGS)

# sax_tests inflates corpus .gz fixtures via zlib.
$(BUILD)/sax_tests: test/sax_tests.c test/gzload.h $(TEST_DEPS) | $(BUILD)
	$(CC) $(TEST_CFLAGS) $(ZLIB_CFLAGS) -o $@ test/sax_tests.c $(CRITERION_LDFLAGS) $(ZLIB_LDFLAGS)

# bind_tests drives the typed-binding state machine from C through the driver
# in test/bind_driver.h, which mirrors the Go driver's yield loop. The extra
# include paths match sources.sh: bind.h pulls the ABI header from abi/, the
# lookup header from vlib, and util/log.h from the repo root. log.c backs the
# -DVJ_DEBUG tracing and trap guards; lookup.c builds fixture lookup blobs.
BIND_TEST_CFLAGS = -g -O0 -march=native -DVJ_DEBUG $(INCLUDES) -I abi -I ../vlib/impl -I .. -I$(CRITERION_INC)
ifeq ($(BIND_ASAN),1)
# Address-only: the production scanner has benign ctz(0) sites that UBSan flags.
BIND_TEST_CFLAGS += -fsanitize=address -fno-omit-frame-pointer
endif
BIND_DRIVER_DEPS = test/bind_driver.h test/bind_fixture.h
BIND_EXTRA_SRC = ../util/log.c ../vlib/impl/vlib/lookup.c

$(BUILD)/bind_tests: test/bind_tests.c $(BIND_DRIVER_DEPS) $(TEST_DEPS) $(BIND_EXTRA_SRC) | $(BUILD)
	$(CC) $(BIND_TEST_CFLAGS) -o $@ test/bind_tests.c $(BIND_EXTRA_SRC) $(CRITERION_LDFLAGS)

# Standalone driver for single-step debugging: run one named fixture against
# JSON on stdin and dump the yield trace, tape, and bound results.
bind-debug: $(BUILD)/bind_debug

$(BUILD)/bind_debug: test/bind_debug.c test/bind_scenarios.h $(BIND_DRIVER_DEPS) $(TEST_DEPS) $(BIND_EXTRA_SRC) | $(BUILD)
	$(CC) $(BIND_TEST_CFLAGS) -o $@ test/bind_debug.c $(BIND_EXTRA_SRC)

bench: bench-build
	@test -n "$${BENCH_PAYLOAD:-}" || { echo "ERROR: BENCH_PAYLOAD is required" >&2; exit 1; }
	nice -20 $(BENCH_TASKSET) $(BUILD)/ndec_sax_dom $(BENCH_PAYLOAD)

bench-build:
	bench/build.sh ndec

jsonfmt: $(BUILD)/jsonfmt

$(BUILD)/jsonfmt: tools/jsonfmt.c impl/ndec/core/fmt.h $(TEST_DEPS) | $(BUILD)
	$(CC) $(CFLAGS) $(INCLUDES) -o $@ tools/jsonfmt.c

jsondata: $(BUILD)/jsondata

$(BUILD)/jsondata: test/jsondata.c test/gzload.h impl/ndec/core/fmt.h $(TEST_DEPS) | $(BUILD)
	$(CC) $(CFLAGS) $(INCLUDES) $(ZLIB_CFLAGS) -o $@ test/jsondata.c $(ZLIB_LDFLAGS)

# Materialize the corpus into build/data for bench/run.sh.
CORPUS_GZ ?= $(REPO_ROOT)/benchmark/corpus/testdata
DATA_DIR  := $(BUILD)/data

data: jsondata
	@mkdir -p $(DATA_DIR)
	$(BUILD)/jsondata "$(CORPUS_GZ)" "$(DATA_DIR)"

debug: $(BUILD)/debug
	$(BUILD)/debug

$(BUILD)/debug: test/debug.c $(TEST_DEPS) | $(BUILD)
	$(CC) -g -O0 $(INCLUDES) -o $@ test/debug.c

clean:
	rm -rf $(BUILD)

# Native object generation and stack-depth validation.
# The default mode exports seven entries from one library: counted DOM
# scan, DOM build, typed bind, streaming bind, window scan, formatter
# parse, and validation.
#
# ndec sets SYSO_PREFIX=ndec + SYSO_ARCH_ONLY=1 in sources.sh, so the
# artifact name drops the mode/isa/os segments: ndec_${arch}.elf, one blob
# per arch shared by every platform. The blob is embedded by
# ndec_blob_{amd64,arm64}.go and mapped at runtime through native/execblob,
# never linked, so SYSO_EXT=".elf" keeps the Go tool from picking it up as a
# host object. The prelink intermediate derives from the artifact basename
# (ndec_${arch}.${ext}); it is a build artifact consumed by stackdepth, not
# an input to Go. Single mode (default), single dylib. NOSPLIT trampolines
# (trampoline_{amd64,arm64,windows_amd64}.s) call through function pointers
# into the seven exported ndec_* symbols.

REPO_ROOT ?= ../..

# The blob is arch-canonical: one linux-built ELF serves every OS on that
# arch (see sources.sh and native_single_abi_blob.md). Force TARGET_OS
# regardless of host or command line, so a darwin or windows host build
# produces the same ELF container instead of silently overwriting the shared
# blob with Mach-O / COFF, which execblob cannot parse. Must precede the
# include: common.mk expands _EXT from TARGET_OS at parse time. override wins
# even against command-line assignments, which is exactly what a caller
# accidentally passing TARGET_OS=darwin needs to lose to.
override TARGET_OS := linux

# Every build is a linux cross build now, so the musl sysroot is required
# whenever the host is not linux. Mirror the top-level Makefile's default so
# `make -C native/ndec gen-all-platforms` works without wrapper variables.
LINUX_SYSROOT ?= $(shell test -d /opt/linux/sysroot && echo /opt/linux/sysroot || echo "")

include ../common.mk

SOURCES := native/ndec/sources.sh

MODE  := default
MODES := default

# Platforms this module runs a canonical build for. Every other platform
# consumes one of these two blobs: darwin/arm64 and windows/arm64 share
# ndec_arm64.elf because AAPCS64 and Apple arm64 pass integer arguments
# identically, and darwin/amd64 plus windows/amd64 share ndec_amd64.elf
# through the Win64 to System V bridge in trampoline_windows_amd64.s (see
# SYSO_ARCH_ONLY in sources.sh).
#
# The artifact name carries no os, so the canonical build must run with
# TARGET_OS=linux. Building it elsewhere overwrites the shared blob with that
# platform's object format, which the other consumers cannot parse.
SUPPORTED_PLATFORMS := linux/amd64 linux/arm64

# One canonical linux blob serves every OS, so one canonical stack budget:
# the depths stackdepth measures on the linux prelink dylib are exactly what
# every consumer executes.
#
# The two bind entries run below a stackReserve prologue (ndec.go calls it
# before the trampoline): a splitting frame whose check verifies headroom for
# its whole extent, on top of the standard nosplit budget every NOSPLIT
# trampoline already relies on. Their chain budget is the sum. The reserve
# sizes live in trampoline_*.s (amd64 $512, arm64 $256) and must stay in
# sync with the values below.
STACK_RESERVE_amd64 := 512
STACK_RESERVE_arm64 := 256
STACK_RESERVE_BUDGET := $(shell echo $$(( $(STACK_BUDGET) + $(STACK_RESERVE_$(TARGET_ARCH)) )))
STACK_CHECK_BUDGETS := \
	ndec_dom_parse_counted=$(STACK_BUDGET) \
	ndec_dom_build=$(STACK_BUDGET) \
	ndec_bind_parse=$(STACK_RESERVE_BUDGET) \
	ndec_bind_parse_stream=$(STACK_RESERVE_BUDGET) \
	ndec_window_scan=$(STACK_BUDGET) \
	ndec_fmt_parse=$(STACK_BUDGET) \
	ndec_valid=$(STACK_BUDGET)

# SYSO_ARCH_ONLY -> artifact is ndec_${arch}.elf, and the prelink intermediate
# (base-0 DWARF companion for stackdepth and addr2line) derives from that same
# basename: ndec_${arch}.${_EXT}. TARGET_OS is forced to linux above, so this
# is always the .so from the canonical blob build.
DYLIB := $(REPO_ROOT)/build/prelink/ndec_$(TARGET_ARCH).$(_EXT)

gen:
	@PROFILE="$(PROFILE)" TOOLCHAIN="$(TOOLCHAIN)" LINUX_SYSROOT="$(LINUX_SYSROOT)" \
	bash $(REPO_ROOT)/scripts/gen-natives.sh $(if $(ASM),--asm) $(GEN_NATIVE_PRELINK_FLAG) \
		$(SOURCES) "$(TARGET_OS)" "$(TARGET_ARCH)"

gen-debug:
	@EXTRA_CFLAGS="-DVJ_DEBUG" DEBUG_SYMBOLS=1 NO_NDEBUG=1 TOOLCHAIN="$(TOOLCHAIN)" LINUX_SYSROOT="$(LINUX_SYSROOT)" \
	bash $(REPO_ROOT)/scripts/gen-natives.sh $(if $(ASM),--asm) $(GEN_NATIVE_PRELINK_FLAG) \
		$(SOURCES) "$(TARGET_OS)" "$(TARGET_ARCH)"

gen-pgo-instr-use:
	@TOOLCHAIN="$(TOOLCHAIN)" LINUX_SYSROOT="$(LINUX_SYSROOT)" \
	bash $(REPO_ROOT)/scripts/gen-natives.sh --pgo-instr-use $(if $(ASM),--asm) $(GEN_NATIVE_PRELINK_FLAG) \
		$(SOURCES) "$(TARGET_OS)" "$(TARGET_ARCH)"

stack-check:
	@set -e; for eb in $(STACK_CHECK_BUDGETS); do \
		e=$${eb%%=*}; b=$${eb##*=}; \
		$(REPO_ROOT)/build/bin/stackdepth $(if $(TREE),-tree,-v) -budget $$b "$(DYLIB)" $$e; \
	done

# Single mode; kept for symmetric interface with encvm.
stack-check-all: stack-check

gen-all-platforms:
	@for p in $(SUPPORTED_PLATFORMS); do \
		os=$${p%%/*}; arch=$${p##*/}; \
		echo "  --- $$os/$$arch ---"; \
		$(MAKE) gen TARGET_OS=$$os TARGET_ARCH=$$arch || exit 1; \
	done

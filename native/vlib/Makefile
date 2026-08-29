# vlib module Makefile.
#
# Owns syso generation and stack-check for the vlib (lookup) native module.
# Invoked from the top-level Makefile via `$(MAKE) -C native/vlib <target>`
# or directly via `make -C native/vlib <target>` (common.mk supplies host
# defaults).
#
# vlib sets SYSO_PREFIX=vlib in sources.sh, so the syso/dylib name drops the
# mode/isa segments: vlib_${os}_${arch}.<ext>. Single mode (default), single
# dylib. Five NOSPLIT trampolines (trampoline_<os>_<arch>.s) call into the
# five exported ndec_lookup_* build-path symbols; ndec_lookup_find is inline
# only and ndec_lookup_tier_name is exported but has no trampoline, so
# neither appears in STACK_CHECK_ENTRIES.

REPO_ROOT ?= ../..
include ../common.mk

SOURCES := native/vlib/sources.sh

MODE  := default
MODES := default

# Platforms this module ships a trampoline .s for.
SUPPORTED_PLATFORMS := darwin/arm64 linux/amd64 linux/arm64 windows/amd64

# NOSPLIT trampoline targets (vlib_init_<os>_<arch>.go declares these five).
STACK_CHECK_ENTRIES := ndec_lookup_size_for ndec_lookup_scratch_size \
	ndec_lookup_init ndec_lookup_get_tier ndec_lookup_footprint

# SYSO_PREFIX=vlib -> naming is vlib_${os}_${arch}.<ext>, no mode/isa.
DYLIB := $(REPO_ROOT)/build/prelink/vlib_$(TARGET_OS)_$(TARGET_ARCH).$(_EXT)

# vlib's tier builders (window/gperf/hand/table) carry large scratch arrays.
# Letting them inline into try_tier collapses the call chain; the merged frame
# (656B on arm64) fits under 768B, well inside the 800B nosplit chain budget.
# Overrides gen-natives.sh's arm64 default (500B); amd64 default (800B) is
# already above this so the override is a no-op there.
#
# Win64 frames carry ~200B of ABI overhead (shadow space + XMM spills),
# pushing ndec_lookup_init past the 800B nosplit budget; its Go wrapper
# stack-reserves first, so the compile-time proxy threshold relaxes.
ifeq ($(TARGET_OS),windows)
STACK_WARN_SIZE ?= 900
else
STACK_WARN_SIZE ?= 768
endif

# Budget for the stackReserve-backed ndec_lookup_init chain on windows.
WINDOWS_RESERVE_BUDGET ?= 1900

.PHONY: gen gen-debug gen-pgo-instr-use stack-check stack-check-all gen-all-platforms

gen:
	@PROFILE="$(PROFILE)" TOOLCHAIN="$(TOOLCHAIN)" LINUX_SYSROOT="$(LINUX_SYSROOT)" STACK_WARN_SIZE="$(STACK_WARN_SIZE)" \
	bash $(REPO_ROOT)/scripts/gen-natives.sh $(if $(ASM),--asm) $(GEN_NATIVE_PRELINK_FLAG) \
		$(SOURCES) "$(TARGET_OS)" "$(TARGET_ARCH)"

gen-debug:
	@EXTRA_CFLAGS="-DVJ_DEBUG" DEBUG_SYMBOLS=1 NO_NDEBUG=1 TOOLCHAIN="$(TOOLCHAIN)" LINUX_SYSROOT="$(LINUX_SYSROOT)" STACK_WARN_SIZE="$(STACK_WARN_SIZE)" \
	bash $(REPO_ROOT)/scripts/gen-natives.sh $(if $(ASM),--asm) $(GEN_NATIVE_PRELINK_FLAG) \
		$(SOURCES) "$(TARGET_OS)" "$(TARGET_ARCH)"

gen-pgo-instr-use:
	@TOOLCHAIN="$(TOOLCHAIN)" LINUX_SYSROOT="$(LINUX_SYSROOT)" STACK_WARN_SIZE="$(STACK_WARN_SIZE)" \
	bash $(REPO_ROOT)/scripts/gen-natives.sh --pgo-instr-use $(if $(ASM),--asm) $(GEN_NATIVE_PRELINK_FLAG) \
		$(SOURCES) "$(TARGET_OS)" "$(TARGET_ARCH)"

stack-check:
ifeq ($(TARGET_OS),windows)
	@# ndec_lookup_init is stackReserve-backed and checks against
	@# WINDOWS_RESERVE_BUDGET; the other four keep the nosplit budget.
	@$(REPO_ROOT)/build/bin/stackdepth $(if $(TREE),-tree,-v) -budget $(STACK_BUDGET) "$(DYLIB)" \
		ndec_lookup_size_for ndec_lookup_scratch_size ndec_lookup_get_tier ndec_lookup_footprint
	@$(REPO_ROOT)/build/bin/stackdepth $(if $(TREE),-tree,-v) -budget $(WINDOWS_RESERVE_BUDGET) "$(DYLIB)" \
		ndec_lookup_init
else
	@$(REPO_ROOT)/build/bin/stackdepth $(if $(TREE),-tree,-v) -budget $(STACK_BUDGET) "$(DYLIB)" $(STACK_CHECK_ENTRIES)
endif

# Single mode; identical to stack-check. Kept for symmetric interface with
# encvm/ndec so stack-check-all can iterate MODES uniformly.
stack-check-all: stack-check

gen-all-platforms:
	@for p in $(SUPPORTED_PLATFORMS); do \
		os=$${p%%/*}; arch=$${p##*/}; \
		echo "  --- $$os/$$arch ---"; \
		$(MAKE) gen TARGET_OS=$$os TARGET_ARCH=$$arch || exit 1; \
	done

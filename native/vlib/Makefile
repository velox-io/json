# vlib module Makefile.
#
# Owns syso generation and stack-check for the vlib (lookup) native module.
# Invoked from the top-level Makefile via `$(MAKE) -C native/vlib <target>`
# or directly via `make -C native/vlib <target>` (common.mk supplies host
# defaults).
#
# vlib sets SYSO_PREFIX=vlib in sources.sh, so the artifact name drops the
# mode/isa segments, and SYSO_ARCH_ONLY=1 drops the os segment too:
# vlib_${arch}.elf, one blob per arch shared by every platform. The blob is
# embedded by vlib_blob_*.go and mapped at runtime, never linked, so
# SYSO_EXT=".elf" keeps the Go tool from picking it up as a host object.
# The intermediate dylib derives from the artifact basename
# (vlib_${arch}.${ext}); it is a build
# artifact, not an input to Go. Single mode (default), single dylib. Five
# NOSPLIT trampolines (trampoline_arm64.s, trampoline_amd64.s,
# trampoline_windows_amd64.s) call through function pointers into the five
# exported ndec_lookup_* build-path symbols; ndec_lookup_find is inline only
# and ndec_lookup_tier_name is exported but has no trampoline, so neither
# appears in STACK_CHECK_ENTRIES.

REPO_ROOT ?= ../..

# The blob is arch-canonical: one linux-built ELF serves every OS on that
# arch (see sources.sh). Force TARGET_OS regardless of host or command line,
# so a darwin or windows host build produces the same ELF container instead
# of silently overwriting the shared blob with Mach-O / COFF, which execblob
# cannot parse. Must precede the include: common.mk expands _EXT from
# TARGET_OS at parse time. override wins even against command-line
# assignments, which is exactly what a caller accidentally passing
# TARGET_OS=darwin needs to lose to.
override TARGET_OS := linux

# Every build is a linux cross build now, so the musl sysroot is required
# whenever the host is not linux. Mirror the top-level Makefile's default so
# `make -C native/vlib gen-all-platforms` works without wrapper variables.
LINUX_SYSROOT ?= $(shell test -d /opt/linux/sysroot && echo /opt/linux/sysroot || echo "")

include ../common.mk

SOURCES := native/vlib/sources.sh

MODE  := default
MODES := default

# Platforms this module runs a build for. Every other platform consumes one of
# these two blobs: darwin/arm64 shares vlib_arm64.elf because AAPCS64 and
# Apple arm64 pass integer arguments identically, and darwin/amd64 plus
# windows/amd64 share vlib_amd64.elf through the Win64 to System V bridge in
# trampoline_windows_amd64.s (see SYSO_ARCH_ONLY in sources.sh).
#
# The artifact name carries no os, so the canonical build must run with
# TARGET_OS=linux. Building it elsewhere overwrites the shared blob with that
# platform's object format, which the other consumers cannot parse.
SUPPORTED_PLATFORMS := linux/amd64 linux/arm64

# NOSPLIT trampoline targets (vlib_init.go declares these five).
STACK_CHECK_ENTRIES := ndec_lookup_size_for ndec_lookup_scratch_size \
	ndec_lookup_init ndec_lookup_get_tier ndec_lookup_footprint

# SYSO_ARCH_ONLY -> artifact is vlib_${arch}.elf, and the prelink intermediate
# (base-0 DWARF companion for stackdepth and addr2line) derives from that same
# basename: vlib_${arch}.${_EXT}. TARGET_OS is forced to linux above, so this
# is always the .so from the canonical blob build.
DYLIB := $(REPO_ROOT)/build/prelink/vlib_$(TARGET_ARCH).$(_EXT)

# vlib's tier builders (window/gperf/hand/table) carry large scratch arrays.
# Letting them inline into try_tier collapses the call chain; the merged frame
# (656B on arm64) fits under 768B, well inside the 800B nosplit chain budget.
# Overrides gen-natives.sh's arm64 default (500B); amd64 default (800B) is
# already above this so the override is a no-op there.
STACK_WARN_SIZE ?= 768

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
	@$(REPO_ROOT)/build/bin/stackdepth $(if $(TREE),-tree,-v) -budget $(STACK_BUDGET) "$(DYLIB)" $(STACK_CHECK_ENTRIES)

# Single mode; identical to stack-check. Kept for symmetric interface with
# encvm/ndec so stack-check-all can iterate MODES uniformly.
stack-check-all: stack-check

gen-all-platforms:
	@for p in $(SUPPORTED_PLATFORMS); do \
		os=$${p%%/*}; arch=$${p##*/}; \
		echo "  --- $$os/$$arch ---"; \
		$(MAKE) gen TARGET_OS=$$os TARGET_ARCH=$$arch || exit 1; \
	done

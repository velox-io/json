# encvm module Makefile.
#
# Owns blob generation and stack-check for the encvm (encode VM) native
# module. Invoked from the top-level Makefile via `$(MAKE) -C native/encvm
# <target>` with REPO_ROOT/TARGET_OS/TARGET_ARCH/TOOLCHAIN/... passed in, or
# directly via `make -C native/encvm <target>` (common.mk supplies host
# defaults).
#
# encvm ships one arch-canonical ELF blob per arch (encvm_amd64.elf /
# encvm_arm64.elf) that merges all three mode specializations (full/compact/
# fast), each exporting a stable vj_vm_exec_<mode> entry. The blob is
# embedded by encvm_blob_*.go and mapped at runtime through native/execblob,
# never linked, so SYSO_EXT=".elf" keeps the Go tool from picking it up as a
# host object. The prelink intermediate derives from the artifact basename
# (encvm_${arch}.${ext}); it is a build artifact consumed by stackdepth, not
# an input to Go. NOSPLIT trampolines (trampoline_{amd64,arm64,
# windows_amd64}.s) call through function pointers into the three entries.

REPO_ROOT ?= ../..

# The blob is arch-canonical: one linux-built ELF serves every OS on that
# arch (see sources.sh and .local/project-docs/native_single_abi_blob.md).
# Force TARGET_OS regardless of host or command line, so a darwin or windows
# host build produces the same ELF container instead of silently overwriting
# the shared blob with Mach-O / COFF, which execblob cannot parse. Must
# precede the include: common.mk expands _EXT from TARGET_OS at parse time.
# override wins even against command-line assignments, which is exactly what
# a caller accidentally passing TARGET_OS=darwin needs to lose to.
override TARGET_OS := linux

# Every build is a linux cross build now, so the musl sysroot is required
# whenever the host is not linux. Mirror the top-level Makefile's default so
# `make -C native/encvm gen-all-platforms` works without wrapper variables.
LINUX_SYSROOT ?= $(shell test -d /opt/linux/sysroot && echo /opt/linux/sysroot || echo "")

include ../common.mk

SOURCES := native/encvm/sources.sh

# All three modes live in the merged blob; MODE selects nothing at build
# time anymore. Kept as a constrained variable so a forwarded MODE=... from
# the top level is harmlessly ignored instead of changing artifact naming.
MODE  := default
MODES := full compact fast

# Platforms this module runs a canonical build for. Every other platform
# consumes one of these two blobs: darwin/arm64 and windows/arm64 share
# encvm_arm64.elf because AAPCS64 and Apple arm64 pass integer arguments
# identically, and darwin/amd64 plus windows/amd64 share encvm_amd64.elf
# through the Win64 to System V bridge in trampoline_windows_amd64.s (see
# SYSO_ARCH_ONLY in sources.sh).
SUPPORTED_PLATFORMS := linux/amd64 linux/arm64

# NOSPLIT trampoline targets (encvm_init.go declares these three).
STACK_CHECK_ENTRIES := vj_vm_exec_full vj_vm_exec_fast vj_vm_exec_compact

# One canonical linux blob serves every OS, so one canonical stack budget
# per entry: the depths stackdepth measures on the linux prelink .so are
# exactly what every consumer executes. The windows bridge frame (~48B of
# shadow space and alignment slack) is absorbed by the larger stackReserve
# frame in trampoline_windows_amd64.s and windows's bigger stackSystem guard.
# The full mode carries the deepest chain (indent handling); fast and
# compact compile the indent paths out.
STACK_CHECK_BUDGETS := vj_vm_exec_full=900 vj_vm_exec_fast=900 vj_vm_exec_compact=900

# SYSO_ARCH_ONLY -> artifact is encvm_${arch}.elf, and the prelink
# intermediate (base-0 DWARF companion for stackdepth and addr2line) derives
# from that same basename: encvm_${arch}.${_EXT}. TARGET_OS is forced to
# linux above, so this is always the .so from the canonical blob build.
DYLIB := $(REPO_ROOT)/build/prelink/encvm_$(TARGET_ARCH).$(_EXT)

.PHONY: gen gen-debug gen-pgo-instr-use stack-check stack-check-all gen-all-platforms

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

# All three entries live in the one merged blob; identical to stack-check.
# Kept for symmetric interface with ndec so stack-check-all can iterate
# modules uniformly.
stack-check-all: stack-check

gen-all-platforms:
	@for p in $(SUPPORTED_PLATFORMS); do \
		os=$${p%%/*}; arch=$${p##*/}; \
		echo "  --- $$os/$$arch ---"; \
		$(MAKE) gen TARGET_OS=$$os TARGET_ARCH=$$arch || exit 1; \
	done

# encvm module Makefile.
#
# Owns syso generation and stack-check for the encvm (encode VM) native
# module. Invoked from the top-level Makefile via `$(MAKE) -C native/encvm
# <target>` with REPO_ROOT/TARGET_OS/TARGET_ARCH/TOOLCHAIN/... passed in, or
# directly via `make -C native/encvm <target>` (common.mk supplies host
# defaults).
#
# encvm ships one syso per (mode, isa): full/compact/fast × neon/avx2. Each
# mode exports a distinct entry symbol vj_vm_exec_<mode>_<isa> that the Go
# NOSPLIT trampoline (trampoline_<os>_<arch>.s) tail-calls. stack-check
# defaults to the `full` mode; stack-check-all walks all three modes.

REPO_ROOT ?= ../..
include ../common.mk

# Per-mode stack budgets. On windows every exec entry is stackReserve
# backed: the Go wrapper runs a goroutine-growth stub ($1280 frame,
# hard ceiling ~1224 after the Go-side frames) before the NOSPLIT
# trampoline, so each mode's budget tracks its measured chain plus a
# small margin and any deepening fails the check. Other platforms keep
# the shared 900B nosplit budget on every mode.
STACK_BUDGET_full    = 900
STACK_BUDGET_compact = 900
STACK_BUDGET_fast    = 900
ifeq ($(TARGET_OS),windows)
STACK_BUDGET_full    = 1200
STACK_BUDGET_compact = 1150
STACK_BUDGET_fast    = 1100
endif
STACK_BUDGET = $(STACK_BUDGET_$(MODE))

SOURCES := native/encvm/sources.sh

MODE  ?= full
MODES := full compact fast

# Platforms this module ships a trampoline .s for.
SUPPORTED_PLATFORMS := darwin/arm64 linux/amd64 linux/arm64 windows/amd64

# Entry symbol the NOSPLIT trampoline tail-calls, per mode × isa.
STACK_CHECK_ENTRIES := vj_vm_exec_$(MODE)_$(_ISA)

# Prelink dylib (full symbols for stackdepth). No SYSO_PREFIX in sources.sh,
# so naming is ${basename}_${mode}_${isa}_${os}_${arch}.<ext> with basename
# taken from SOURCE_FILE (encvm.c -> encvm).
DYLIB := $(REPO_ROOT)/build/prelink/encvm_$(MODE)_$(_ISA)_$(TARGET_OS)_$(TARGET_ARCH).$(_EXT)

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
	@$(REPO_ROOT)/build/bin/stackdepth $(if $(TREE),-tree,-v) -budget $(STACK_BUDGET) "$(DYLIB)" $(STACK_CHECK_ENTRIES)

stack-check-all:
	@for m in $(MODES); do $(MAKE) stack-check MODE=$$m || exit 1; done

gen-all-platforms:
	@for p in $(SUPPORTED_PLATFORMS); do \
		os=$${p%%/*}; arch=$${p##*/}; \
		echo "  --- $$os/$$arch ---"; \
		$(MAKE) gen TARGET_OS=$$os TARGET_ARCH=$$arch || exit 1; \
	done

# Shared build detection for native module Makefiles.
#
# Included by native/{encvm,ndec,vlib}/Makefile. Supplies host platform
# defaults, the ISA tag used in syso/dylib naming (arm64->neon, amd64->avx2),
# the prelink artifact extension per OS, and the gen-natives.sh flag
# fragment (GEN_NATIVE_PRELINK_FLAG) for NO_PRELINK.
#
# When invoked from the top-level Makefile these are all passed in as
# command-line vars, so the ?= defaults are skipped. When a module Makefile
# is invoked directly (make -C native/<mod> gen), common.mk provides host
# defaults so the recipe works without the top-level wrapper.

_HOST_OS   := $(shell uname -s | tr '[:upper:]' '[:lower:]')
_HOST_ARCH := $(shell uname -m)
ifeq ($(_HOST_ARCH),x86_64)
  _HOST_ARCH := amd64
else ifeq ($(_HOST_ARCH),aarch64)
  _HOST_ARCH := arm64
endif

TARGET_OS   ?= $(_HOST_OS)
TARGET_ARCH ?= $(_HOST_ARCH)

# ISA tag embedded in syso/dylib names and entry symbol suffixes.
# Mirrors scripts/gen-natives.sh get_available_isas().
ifeq ($(filter arm64,$(TARGET_ARCH)),arm64)
_ISA := neon
else ifeq ($(filter amd64,$(TARGET_ARCH)),amd64)
_ISA := avx2
else
$(error unsupported TARGET_ARCH=$(TARGET_ARCH))
endif

# Prelink artifact extension per platform. stackdepth accepts all three.
ifeq ($(filter darwin,$(TARGET_OS)),darwin)
_EXT := dylib
else ifeq ($(filter linux,$(TARGET_OS)),linux)
_EXT := so
else ifeq ($(filter windows,$(TARGET_OS)),windows)
_EXT := dll
else
$(error unsupported TARGET_OS=$(TARGET_OS))
endif

# NO_PRELINK=1 -> pass --no-prelink to gen-natives.sh.
GEN_NATIVE_PRELINK_FLAG ?= $(if $(NO_PRELINK),--no-prelink,)

# NO_OPT is read directly by gen-natives.sh from the environment; export it so
# the bash recipe inherits the value forwarded by the top-level Makefile.
# (NO_PRELINK is converted to a CLI flag above; NO_OPT has no CLI equivalent
# because it only switches an internal -O3/-O0 variable.)
export NO_OPT

ifeq ($(TARGET_OS),windows)
STACK_BUDGET ?= 800
else
STACK_BUDGET ?= 800
endif

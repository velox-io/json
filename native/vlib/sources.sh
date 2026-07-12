#!/usr/bin/env bash
# lookup module build configuration.
#
# Builds a single .syso that exports the ndec_lookup_* build path
# (size_for / init / get_tier / tier_name / footprint). The hot query
# ndec_lookup_find lives inline in impl/lookup.h and is not part of the
# .syso export surface: consumers embed the header directly.
#
# Sourced by gen-natives.sh; all paths relative to REPO_ROOT.

SOURCE_FILE="native/vlib/impl/vlib/lookup.c"

# Provides memcpy / memset / memcmp / bzero without pulling in libc.
STDLIB_SOURCES="
  native/stdlib/memory.c
  native/stdlib/assert.c
"

EXTRA_SOURCES=""

EXTRA_CFLAGS="-I$REPO_ROOT/native/vlib/impl -I$REPO_ROOT/native"

TARGET_DIR="native/vlib"

# Optional base name for the generated .syso. When set, it replaces the
# default (source file basename) in the .syso file name and the mode/isa
# segments are dropped, yielding "{SYSO_PREFIX}_{os}_{arch}.syso".
# Leave empty to keep the default "{basename}_{mode}_{isa}_{os}_{arch}.syso".
SYSO_PREFIX="vlib"

if [ -z "$MODES" ]; then
  MODES="default"
fi

MODE_FLAGS_default=""

# All exported entry points share the ndec_lookup_ prefix. prelink-obj's
# HasPrefix filter keeps every global symbol that starts with this prefix
# and demotes everything else to local.
EXPORT_SYMBOL_PREFIX_PATTERN="ndec_lookup_"
EXPORT_SYMBOL_NAMES="ndec_lookup_size_for ndec_lookup_scratch_size ndec_lookup_init ndec_lookup_get_tier ndec_lookup_tier_name ndec_lookup_tier_name_ex ndec_lookup_footprint"

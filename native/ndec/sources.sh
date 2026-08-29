#!/usr/bin/env bash

SOURCE_FILE="native/ndec/entry/ndec.c"

STDLIB_SOURCES="
  native/stdlib/memory.c
  native/stdlib/assert.c
"

# util/log.c provides vj_fprintf_stderr
EXTRA_SOURCES="
  native/util/log.c
  native/ndec/entry/fmt.c
"

# Append include paths to any caller-supplied EXTRA_CFLAGS (e.g. -DVJ_DEBUG
# from make gen-debug) instead of overwriting them.
EXTRA_CFLAGS="${EXTRA_CFLAGS:-} -I$REPO_ROOT/native/ndec/impl -I$REPO_ROOT/native/ndec/abi -I$REPO_ROOT/native/vlib/impl -I$REPO_ROOT/native"

TARGET_DIR="native/ndec"

if [ -z "$MODES" ]; then
  MODES="default"
fi

MODE_FLAGS_default=""

# Entry points share the ndec_ prefix. prelink-obj's HasPrefix
# filter keeps every global symbol with this prefix and demotes the rest to
# local, so only ndec_dom_parse_counted / ndec_dom_build /
# ndec_bind_parse / ndec_fmt_parse reach the linker as globals. Impl helpers
# are static inline; stdlib symbols are HIDDEN;
# util/log's vj_fprintf_stderr starts with vj_, not ndec_.
#
# No SYMBOL_RENAMES: the Go trampolines call the stable, ISA-independent
# names (ndec_dom_parse_counted, ndec_dom_build, ndec_bind_parse,
# ndec_fmt_parse) directly.
EXPORT_SYMBOL_PREFIX_PATTERN="ndec_"
EXPORT_SYMBOL_NAMES="ndec_dom_parse_counted ndec_dom_build ndec_bind_parse ndec_fmt_parse"

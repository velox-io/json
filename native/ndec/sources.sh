#!/usr/bin/env bash


SOURCE_FILE="native/ndec/gobind/ndec.c"

STDLIB_SOURCES="
  native/stdlib/memory.c
  native/stdlib/assert.c
"

EXTRA_SOURCES=""

EXTRA_CFLAGS="-I$REPO_ROOT/native/ndec/impl"

TARGET_DIR="native/ndec"

if [ -z "$MODES" ]; then
  MODES="default"
fi

MODE_FLAGS_default=""

EXPORT_SYMBOL_PREFIX="ndec_parse"

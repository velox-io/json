#!/usr/bin/env bash
#
# build.sh — build comparison benchmark binaries (simdjson, yyjson, sonic)
#
# Env overrides:
#   SIMDJSON_SRC   path to simdjson repo  (default: $HOME/Data/projects/simdjson.git)
#   YYJSON_SRC     path to yyjson repo    (default: $HOME/Data/projects/yyjson.git)
#   SONIC_CPP_SRC  path to sonic-cpp repo (default: $HOME/Data/projects/sonic-cpp.git)
#   PAYLOAD        default payload path compiled into the binary
#                  (default: <ndec>/test/data/bench_payload.json)
#   BUILD_DIR      where to emit binaries
#                  (default: <ndec>/build, i.e. native/ndec/build)
#   CXX            C++ compiler (default: clang++)
#   SDKROOT        (macOS only) SDK path. Auto-detected via xcrun when unset;
#                  passed as -isysroot so non-Apple toolchains can find headers.
#
# Usage:
#   test/bench/build.sh            # build all
#   test/bench/build.sh simdjson   # build only simdjson
#   test/bench/build.sh yyjson     # build only yyjson
#   test/bench/build.sh sonic      # build only sonic

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NDEC_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

BUILD_DIR="${BUILD_DIR:-$NDEC_DIR/build}"

: "${SIMDJSON_SRC:=$HOME/Data/projects/simdjson.git}"
: "${YYJSON_SRC:=$HOME/Data/projects/yyjson.git}"
: "${SONIC_CPP_SRC:=$HOME/Data/projects/sonic-cpp.git}"
: "${PAYLOAD:=$NDEC_DIR/test/data/bench_payload.json}"
: "${CXX:=clang++}"

mkdir -p "$BUILD_DIR"

target="${1:-all}"

CFLAGS_COMMON=(-std=c++17 -O3 -march=native -DDEFAULT_PAYLOAD_PATH="\"$PAYLOAD\"")

# On macOS, non-Apple toolchains (e.g. /opt/llvm) don't ship a C library and
# need -isysroot pointing at the Xcode / CommandLineTools SDK. Resolve it once
# via xcrun and feed it to the compiler explicitly; this is a no-op for Apple
# clang but rescues Homebrew/self-built LLVM.
if [[ "$(uname -s)" == "Darwin" ]]; then
  if [[ -z "${SDKROOT:-}" ]]; then
    if command -v xcrun >/dev/null 2>&1; then
      SDKROOT="$(xcrun --show-sdk-path 2>/dev/null || true)"
    fi
  fi
  if [[ -n "${SDKROOT:-}" && -d "$SDKROOT" ]]; then
    export SDKROOT
    CFLAGS_COMMON+=(-isysroot "$SDKROOT")
  fi
fi

build_simdjson() {
  if [[ ! -f "$SIMDJSON_SRC/singleheader/simdjson.cpp" ]]; then
    echo "error: simdjson source not found at $SIMDJSON_SRC/singleheader/simdjson.cpp" >&2
    echo "       set SIMDJSON_SRC to the simdjson repo root" >&2
    return 1
  fi
  echo "==> building $BUILD_DIR/simdjson"
  "$CXX" "${CFLAGS_COMMON[@]}" \
    "$SCRIPT_DIR/simdjson.cpp" \
    "$SIMDJSON_SRC/singleheader/simdjson.cpp" \
    -I"$SIMDJSON_SRC/singleheader" \
    -o "$BUILD_DIR/simdjson"
}

build_yyjson() {
  if [[ ! -f "$YYJSON_SRC/src/yyjson.c" ]]; then
    echo "error: yyjson source not found at $YYJSON_SRC/src/yyjson.c" >&2
    echo "       set YYJSON_SRC to the yyjson repo root" >&2
    return 1
  fi
  echo "==> building $BUILD_DIR/yyjson"
  # yyjson.c is C source; clang++ treats it as C++ and emits a deprecation
  # warning. -Wno-deprecated silences it without affecting codegen.
  "$CXX" "${CFLAGS_COMMON[@]}" -Wno-deprecated \
    "$SCRIPT_DIR/yyjson.cpp" \
    "$YYJSON_SRC/src/yyjson.c" \
    -I"$YYJSON_SRC/src" \
    -o "$BUILD_DIR/yyjson"
}

build_sonic() {
  if [[ ! -f "$SONIC_CPP_SRC/include/sonic/sonic.h" ]]; then
    echo "error: sonic-cpp source not found at $SONIC_CPP_SRC/include/sonic/sonic.h" >&2
    echo "       set SONIC_CPP_SRC to the sonic-cpp repo root" >&2
    return 1
  fi
  echo "==> building $BUILD_DIR/sonic"
  # sonic-cpp is header-only; just point at its include dir.
  "$CXX" "${CFLAGS_COMMON[@]}" \
    "$SCRIPT_DIR/sonic.cpp" \
    -I"$SONIC_CPP_SRC/include" \
    -o "$BUILD_DIR/sonic"
}

case "$target" in
  all)      build_simdjson; build_yyjson; build_sonic ;;
  simdjson) build_simdjson ;;
  yyjson)   build_yyjson ;;
  sonic)    build_sonic ;;
  *) echo "usage: $0 [all|simdjson|yyjson|sonic]" >&2; exit 2 ;;
esac

echo "done. run with:"
[[ "$target" == "all" || "$target" == "simdjson" ]] && \
  echo "  nice -20 $BUILD_DIR/simdjson"
[[ "$target" == "all" || "$target" == "yyjson" ]] && \
  echo "  nice -20 $BUILD_DIR/yyjson"
[[ "$target" == "all" || "$target" == "sonic" ]] && \
  echo "  nice -20 $BUILD_DIR/sonic"

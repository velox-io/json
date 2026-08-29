#!/usr/bin/env bash
#
# bench/build.sh: build every benchmark in this directory.
#
# Categories:
#   ndec-self      tight-loop microbenches over ndec itself
#                  (ndec_sax_base, ndec_sax_dom, ndec_dom_tape,
#                   ndec_dom_s1)
#   comparison     same-payload comparison with third-party engines
#                  (simdjson_dom_tape, yyjson, sonic, simdjson_dom_s1)
#
# Each *.c / *.cpp under bench/ owns its own main() and links payload.h
# directly. No shared harness; backends do not see each other's symbols.
#
# Env overrides:
#   SIMDJSON_SRC   path to simdjson repo  (default: $HOME/Data/projects/simdjson.git)
#   YYJSON_SRC     path to yyjson repo    (default: $HOME/Data/projects/yyjson.git)
#   SONIC_CPP_SRC  path to sonic-cpp repo (default: $HOME/Data/projects/sonic-cpp.git)
#   PAYLOAD        default payload path baked into binaries (optional)
#   BUILD_DIR      output directory       (default: <ndec>/build)
#   CXX            C++ compiler           (default: clang++)
#   CC             C compiler             (default: clang)
#   SDKROOT        (macOS only) SDK path. Auto-detected via xcrun when unset.
#
# Usage:
#   bench/build.sh                       # build everything possible
#   bench/build.sh ndec                  # build all ndec-self benches
#   bench/build.sh comparison            # build all comparison benches
#   bench/build.sh <name>                # build one specific binary
#
# Known target names (one binary per name):
#   ndec_sax_base ndec_sax_dom ndec_dom_tape ndec_dom_s1
#   simdjson_dom_tape yyjson sonic simdjson_dom_s1
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCH_DIR="$SCRIPT_DIR"
NDEC_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
NATIVE_DIR="$(cd "$NDEC_DIR/.." && pwd)"

BUILD_DIR="${BUILD_DIR:-$NDEC_DIR/build}"

: "${SIMDJSON_SRC:=$HOME/Data/projects/simdjson.git}"
: "${YYJSON_SRC:=$HOME/Data/projects/yyjson.git}"
: "${SONIC_CPP_SRC:=$HOME/Data/projects/sonic-cpp.git}"
: "${PAYLOAD:=}"
: "${CXX:=clang++}"
: "${CC:=clang}"

mkdir -p "$BUILD_DIR"

# Common include set for ndec-self benches (so they can pull in ndec/core/*.h
# and payload.h via -I bench).
NDEC_INCS=(-I"$NDEC_DIR/impl" -I"$NATIVE_DIR/include" -I"$BENCH_DIR")

# C flags for ndec-self benches (mirrors what produced the published numbers).
NDEC_CFLAGS=(-O3 -march=native -mllvm -inline-threshold=10000
             -g -fno-omit-frame-pointer)

# C++ flags for comparison benches.
CXX_CFLAGS=(-std=c++17 -O3 -march=native -g)

# On macOS, non-Apple toolchains need -isysroot pointing at the SDK.
if [[ "$(uname -s)" == "Darwin" ]]; then
  if [[ -z "${SDKROOT:-}" ]] && command -v xcrun >/dev/null 2>&1; then
    SDKROOT="$(xcrun --show-sdk-path 2>/dev/null || true)"
  fi
  if [[ -n "${SDKROOT:-}" && -d "$SDKROOT" ]]; then
    export SDKROOT
    NDEC_CFLAGS+=(-isysroot "$SDKROOT")
    CXX_CFLAGS+=(-isysroot "$SDKROOT")
  fi
fi

if [[ -n "${PAYLOAD:-}" ]]; then
  NDEC_CFLAGS+=(-DDEFAULT_PAYLOAD_PATH="\"$PAYLOAD\"")
  CXX_CFLAGS+=(-DDEFAULT_PAYLOAD_PATH="\"$PAYLOAD\"")
fi

# Build helpers.
build_c_self() {
  local name="$1"
  local src="$BENCH_DIR/$1.c"
  shift
  echo "==> $BUILD_DIR/$name"
  "$CC" "${NDEC_CFLAGS[@]}" "${NDEC_INCS[@]}" "$@" -o "$BUILD_DIR/$name" "$src"
}

# ndec-self benchmarks
build_ndec_sax_base()           { build_c_self ndec_sax_base; }
build_ndec_sax_dom()            { build_c_self ndec_sax_dom; }
build_ndec_dom_s1()    { build_c_self ndec_dom_s1; }

# ndec_dom_tape runs both string-storage flavors (copy / zc) in one
# binary, selected by a runtime mode constant.
build_ndec_dom_tape() {
  echo "==> $BUILD_DIR/ndec_dom_tape"
  "$CC" "${NDEC_CFLAGS[@]}" "${NDEC_INCS[@]}" -o "$BUILD_DIR/ndec_dom_tape" "$BENCH_DIR/ndec_dom_tape.c"
}

# Comparison benchmarks
build_simdjson_dom_tape() {
  if [[ ! -f "$SIMDJSON_SRC/singleheader/simdjson.cpp" ]]; then
    echo "error: simdjson source not found at $SIMDJSON_SRC/singleheader/simdjson.cpp" >&2
    echo "       set SIMDJSON_SRC to the simdjson repo root" >&2
    return 1
  fi
  echo "==> $BUILD_DIR/simdjson_dom_tape"
  "$CXX" "${CXX_CFLAGS[@]}" -I"$BENCH_DIR" \
    "$BENCH_DIR/simdjson_dom_tape.cpp" \
    "$SIMDJSON_SRC/singleheader/simdjson.cpp" \
    -I"$SIMDJSON_SRC/singleheader" \
    -o "$BUILD_DIR/simdjson_dom_tape"
}

build_simdjson_dom_s1() {
  if [[ ! -f "$SIMDJSON_SRC/singleheader/simdjson.cpp" ]]; then
    echo "error: simdjson source not found at $SIMDJSON_SRC/singleheader/simdjson.cpp" >&2
    return 1
  fi
  echo "==> $BUILD_DIR/simdjson_dom_s1"
  "$CXX" "${CXX_CFLAGS[@]}" \
    "$BENCH_DIR/simdjson_dom_s1.cpp" \
    "$SIMDJSON_SRC/singleheader/simdjson.cpp" \
    -I"$SIMDJSON_SRC/singleheader" \
    -o "$BUILD_DIR/simdjson_dom_s1"
}

build_yyjson() {
  if [[ ! -f "$YYJSON_SRC/src/yyjson.c" ]]; then
    echo "error: yyjson source not found at $YYJSON_SRC/src/yyjson.c" >&2
    echo "       set YYJSON_SRC to the yyjson repo root" >&2
    return 1
  fi
  echo "==> $BUILD_DIR/yyjson"
  "$CXX" "${CXX_CFLAGS[@]}" -Wno-deprecated -I"$BENCH_DIR" \
    "$BENCH_DIR/yyjson.cpp" \
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
  echo "==> $BUILD_DIR/sonic"
  "$CXX" "${CXX_CFLAGS[@]}" -I"$BENCH_DIR" \
    "$BENCH_DIR/sonic.cpp" \
    -I"$SONIC_CPP_SRC/include" \
    -o "$BUILD_DIR/sonic"
}

build_ndec_group() {
  build_ndec_sax_base
  build_ndec_sax_dom
  build_ndec_dom_s1
  build_ndec_dom_tape
}

build_comparison_group() {
  build_simdjson_dom_tape
  build_simdjson_dom_s1
  build_yyjson
  build_sonic
}

build_all() {
  build_ndec_group
  build_comparison_group
}

target="${1:-all}"
case "$target" in
  all)                          build_all ;;
  ndec|ndec-self)               build_ndec_group ;;
  comparison|compare)           build_comparison_group ;;
  ndec_sax_base)                build_ndec_sax_base ;;
  ndec_sax_dom)                 build_ndec_sax_dom ;;
  ndec_dom_tape)                build_ndec_dom_tape ;;
  ndec_dom_s1)         build_ndec_dom_s1 ;;
  simdjson_dom_tape)             build_simdjson_dom_tape ;;
  simdjson_dom_s1)               build_simdjson_dom_s1 ;;
  yyjson)                       build_yyjson ;;
  sonic)                        build_sonic ;;
  *)
    echo "usage: $0 [all|ndec|comparison|<binary-name>]" >&2
    exit 2
    ;;
esac

echo "done."

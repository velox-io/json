#!/usr/bin/env bash
#
# gen-rsdec.sh - Compile Rust rsdec sources into .syso for Go linking
#
# Usage:
#   ./gen-rsdec.sh [--debug] [target_os] [target_arch]
#
# Options:
#   --debug  - Build with debug assertions and symbols
#
# The script:
#   1. Runs `cargo build --release` to produce a staticlib (.a)
#   2. Extracts the .o from the .a
#   3. Uses prelink.sh to produce a zero-relocation .syso
#
# Output:
#   native/rsdec/rsdec_<os>_<arch>.syso

set -e

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# ============================================================
#  Parse options
# ============================================================

DEBUG_BUILD=false
while [[ "${1:-}" == --* ]]; do
    case "$1" in
        --debug) DEBUG_BUILD=true; shift ;;
        *)       echo "Error: Unknown option: $1"; exit 1 ;;
    esac
done

# ============================================================
#  Platform detection
# ============================================================

HOST_OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$HOST_OS" in
    darwin)              HOST_OS="darwin" ;;
    linux)               HOST_OS="linux" ;;
    mingw*|msys*|cygwin*) HOST_OS="windows" ;;
esac

HOST_ARCH=$(uname -m)
case "$HOST_ARCH" in
    arm64|aarch64) HOST_ARCH="arm64" ;;
    x86_64|amd64)  HOST_ARCH="amd64" ;;
esac

TARGET_OS="${1:-$HOST_OS}"
TARGET_ARCH="${2:-$HOST_ARCH}"

# ============================================================
#  Rust target triple mapping
# ============================================================

get_rust_target() {
    local os=$1
    local arch=$2
    case "$os-$arch" in
        darwin-arm64)  echo "aarch64-apple-darwin" ;;
        darwin-amd64)  echo "x86_64-apple-darwin" ;;
        linux-arm64)   echo "aarch64-unknown-linux-gnu" ;;
        linux-amd64)   echo "x86_64-unknown-linux-gnu" ;;
        *)             echo "Error: unsupported target: $os/$arch" >&2; exit 1 ;;
    esac
}

# Zig target triple (for prelink.sh)
get_zig_target() {
    local os=$1
    local arch=$2
    case "$arch" in
        amd64) arch="x86_64" ;;
        arm64) arch="aarch64" ;;
    esac
    case "$os" in
        darwin)  echo "${arch}-macos" ;;
        linux)   echo "${arch}-linux" ;;
        windows) echo "${arch}-windows" ;;
        *)       echo "${arch}-${os}" ;;
    esac
}

get_isa() {
    local os=$1
    local arch=$2
    case "$arch" in
        arm64) echo "neon" ;;
        amd64) echo "sse42" ;;
        *)     echo "" ;;
    esac
}

RUST_TARGET=$(get_rust_target "$TARGET_OS" "$TARGET_ARCH")
ZIG_TARGET=$(get_zig_target "$TARGET_OS" "$TARGET_ARCH")
ISA=$(get_isa "$TARGET_OS" "$TARGET_ARCH")

IMPL_DIR="$REPO_ROOT/native/rsdec/impl"
TARGET_DIR="$REPO_ROOT/native/rsdec"
BUILD_DIR="$REPO_ROOT/build/native/rsdec"
mkdir -p "$BUILD_DIR"

echo "Building rsdec for: $TARGET_OS/$TARGET_ARCH (rust target: $RUST_TARGET)"

# ============================================================
#  Step 1: Cargo build
# ============================================================

CARGO_PROFILE="release"
CARGO_FLAGS="--release"

# Avoid jump tables and memcmp linkage — they generate ADRP relocations
# that the prelinker cannot always resolve correctly in the .syso.
export RUSTFLAGS="${RUSTFLAGS:-} -C llvm-args=-aarch64-min-jump-table-entries=999"

if [ "$DEBUG_BUILD" = true ]; then
    echo "  Debug build enabled"
    # Still use release profile but with debug assertions
    export RUSTFLAGS="${RUSTFLAGS:-} -C debug-assertions=yes"
fi

echo "  Building Rust staticlib..."
(cd "$IMPL_DIR" && cargo build --lib $CARGO_FLAGS --target "$RUST_TARGET" 2>&1)

# Find the .a file
STATIC_LIB="$IMPL_DIR/target/$RUST_TARGET/$CARGO_PROFILE/libvjson_rsdec.a"
if [ ! -f "$STATIC_LIB" ]; then
    echo "Error: staticlib not found: $STATIC_LIB"
    echo "  Expected cargo to produce: libvjson_rsdec.a"
    ls -la "$IMPL_DIR/target/$RUST_TARGET/$CARGO_PROFILE/"*.a 2>/dev/null || true
    exit 1
fi

echo "  Static lib: $STATIC_LIB"

# ============================================================
#  Step 2: Prelink .a → .syso
#
#  Pass the .a directly to the linker (via prelink.sh). The linker
#  selectively pulls in only the objects that resolve undefined symbols,
#  avoiding unused compiler_builtins (e.g. rust_eh_personality).
# ============================================================

SYSO_NAME="rsdec_${TARGET_OS}_${TARGET_ARCH}.syso"
SYSO_PATH="$TARGET_DIR/$SYSO_NAME"

echo "  Prelinking → $SYSO_NAME..."

# Export list: only keep vj_dec_* as global symbols
EXPORT_LIST="$BUILD_DIR/_exports_rsdec.txt"
if [ "$TARGET_OS" = "darwin" ]; then
    printf '_vj_dec_exec\n_vj_dec_resume\n' > "$EXPORT_LIST"
else
    printf 'vj_dec_exec\nvj_dec_resume\n' > "$EXPORT_LIST"
fi

"$REPO_ROOT/scripts/prelink.sh" \
    -o "$SYSO_PATH" \
    -t "$ZIG_TARGET" \
    -i "$ISA" \
    -e "$EXPORT_LIST" \
    "$STATIC_LIB"

echo ""
echo "Generated files:"
echo "  $SYSO_PATH"
echo ""
echo "Done!"

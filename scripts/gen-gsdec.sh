#!/usr/bin/env bash
#
# gen-gsdec.sh - Compile C gsdec sources into .syso for Go linking
#
# Usage:
#   ./gen-gsdec.sh [target_os] [target_arch]
#
# Output:
#   native/gsdec/gsdec_<os>_<arch>.syso

set -e

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# ============================================================
#  Platform detection
# ============================================================

HOST_OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$HOST_OS" in
    darwin)              HOST_OS="darwin" ;;
    linux)               HOST_OS="linux" ;;
esac

HOST_ARCH=$(uname -m)
case "$HOST_ARCH" in
    arm64|aarch64) HOST_ARCH="arm64" ;;
    x86_64|amd64)  HOST_ARCH="amd64" ;;
esac

TARGET_OS="${1:-$HOST_OS}"
TARGET_ARCH="${2:-$HOST_ARCH}"

echo "Building gsdec for: ${TARGET_OS}/${TARGET_ARCH}"

# ============================================================
#  Build directories
# ============================================================

SRC_DIR="$REPO_ROOT/native/gsdec/impl"
TARGET_DIR="$REPO_ROOT/native/gsdec"
BUILD_DIR="$REPO_ROOT/build/gsdec"
mkdir -p "$BUILD_DIR"

# ============================================================
#  Compiler / target setup
# ============================================================

case "$TARGET_ARCH" in
    arm64)
        CLANG_TARGET="aarch64-apple-darwin"
        ZIG_TARGET="aarch64-macos"
        ISA="neon"
        ;;
    amd64)
        CLANG_TARGET="x86_64-apple-darwin"
        ZIG_TARGET="x86_64-macos"
        ISA="sse42"
        ;;
esac

# ============================================================
#  Step 1: Compile C → .o
# ============================================================

OBJ_FILE="$BUILD_DIR/gsdec.o"
echo "  Compiling C..."
clang -O2 -ffreestanding -nostdlib -c \
    -target "$CLANG_TARGET" \
    "$SRC_DIR/gsdec.c" \
    -o "$OBJ_FILE"

echo "  Object: $OBJ_FILE ($(wc -c < "$OBJ_FILE" | tr -d ' ') bytes)"

# ============================================================
#  Step 2: Prelink .o → .syso
# ============================================================

SYSO_NAME="gsdec_${TARGET_OS}_${TARGET_ARCH}.syso"
SYSO_PATH="$TARGET_DIR/$SYSO_NAME"

echo "  Prelinking → $SYSO_NAME..."

# Export list: only keep vj_gdec_* as global symbols
EXPORT_LIST="$BUILD_DIR/_exports_gsdec.txt"
if [ "$TARGET_OS" = "darwin" ]; then
    printf '_vj_gdec_exec\n_vj_gdec_resume\n' > "$EXPORT_LIST"
else
    printf 'vj_gdec_exec\nvj_gdec_resume\n' > "$EXPORT_LIST"
fi

"$REPO_ROOT/scripts/prelink.sh" \
    -o "$SYSO_PATH" \
    -t "$ZIG_TARGET" \
    -i "$ISA" \
    -e "$EXPORT_LIST" \
    "$OBJ_FILE"

echo ""
echo "Generated files:"
echo "  $SYSO_PATH"
echo ""
echo "Done!"

#!/usr/bin/env bash
#
# Run Go tests under the AddressSanitizer (-asan) flag.
#
# Usage:
#   scripts/test-asan.sh [options] [-- extra go test flags]
#
# Options:
#   -p <pkg>      Go package path to test. Default: ./tests/
#   -r <regex>    -run regex. Default: TestBuildDiverseTypesRace
#   -n <count>    -count. Default: 3000
#   -t <tags>     Comma-separated -tags. Default: vj_nomapprewire
#   -g <gcflags>  -gcflags. Default: -d=checkptr
#   -G <gogc>     GOGC value (off = unset). Default: 1
#   -k <pkgs>     nix-shell -p packages providing clang + compiler-rt.
#                 Default: "clang_19 llvmPackages_19.compiler-rt"
#   -h            Show this help.
#
# Examples:
#   scripts/nix-asan-run.sh
#   scripts/nix-asan-run.sh -n 3                          # quick smoke
#   scripts/nix-asan-run.sh -r TestSomeOther -n 100
#   scripts/nix-asan-run.sh -- -v                         # pass -v through to go test

set -euo pipefail

PKG="./tests/"
RUN="TestBuildDiverseTypesRace"
COUNT=3000
TAGS="vj_nomapprewire"
GCFLAGS="-d=checkptr"
GOGC_VAL="1"
NIX_PKGS="clang_19 llvmPackages_19.compiler-rt"

usage() {
    # Print the header comment block (everything before `set -euo pipefail`)
    # with the leading "# " stripped, skipping the shebang.
    awk '
        /^set -euo pipefail/ { exit }
        NR == 1 { next }                       # skip shebang
        /^#/  { sub(/^# ?/, ""); print; next }
        /^$/  { next }                         # skip blank spacers
    ' "$0"
    exit "${1:-0}"
}

while getopts ":p:r:n:t:g:G:k:h" opt; do
    case "$opt" in
        p) PKG="$OPTARG" ;;
        r) RUN="$OPTARG" ;;
        n) COUNT="$OPTARG" ;;
        t) TAGS="$OPTARG" ;;
        g) GCFLAGS="$OPTARG" ;;
        G) GOGC_VAL="$OPTARG" ;;
        k) NIX_PKGS="$OPTARG" ;;
        h) usage 0 ;;
        \?) echo "unknown option: -$OPTARG" >&2; usage 1 ;;
        :)  echo "option -$OPTARG requires an argument" >&2; usage 1 ;;
    esac
done
shift $((OPTIND - 1))

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

# nix-shell sets CC to gcc by default; we override with the wrapper-resolved
# clang so that -print-file-name=libclang_rt.asan-x86_64.a returns a real path.
nix-shell -p $NIX_PKGS --run "$(cat <<EOF
set -euo pipefail
export CC="\$(command -v clang)"
echo "CC=\$CC"
\$CC --version | head -2
${GOGC_VAL:+export GOGC="$GOGC_VAL"}
go test -tags "$TAGS" "$PKG" \\
    -run "$RUN" -count="$COUNT" \\
    -gcflags="$GCFLAGS" -asan "$@"
EOF
)"

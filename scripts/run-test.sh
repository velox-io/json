#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

cd "$REPO_ROOT"


go test . -count=1
go test -race . -count=1

go test ./vbind ./decode/bind/ ./native/ndec ./native/vlib -count=1
go test -race ./vbind ./decode/bind/ ./native/ndec ./native/vlib -count=1

# The pure-Go decoder round (vj_nondec) is platform independent and runs once
# per Go version in its own CI job: scripts/run-test-nondec.sh, make test-nondec.

# Engine differential round: a native build whose engine choice is dynamic, so
# the diff suites run both engines over each input in one process.
go test -tags vj_enginediff ./decode/bind/ -count=1
go test -race -tags vj_enginediff ./decode/bind/ -count=1

go test ./venc -count=1
go test -race ./venc -count=1
go test -tags vj_noencvm ./venc -count=1
go test -race -tags vj_noencvm ./venc -count=1
go test -tags vjgcstress ./venc -count=1
go test -race -tags vjgcstress ./venc -count=1
go test -tags vjstress ./venc -count=1
go test -race -tags vjstress ./venc -count=1

go test ./stream -count=1
go test -race ./stream -count=1
go test -tags vj_noencvm ./stream -count=1
go test -race -tags vj_noencvm ./stream -count=1

go test ./tests/ -count=1
go test -race ./tests/ -count=1
go test -tags vj_noencvm ./tests -count=1
go test -tags vj_noencvm -race ./tests -count=1
go test -tags vjgcstress ./tests -count=1
go test -race -tags vjgcstress ./tests -count=1
go test -tags vjstackstress ./tests/stackstress/ -count=1
go test -race -tags vjstackstress ./tests/stackstress/ -count=1

go test ./tests/compat/ -count=1
go test -race ./tests/compat/ -count=1

# Single-process shuffled run of the public-API suites (tests, tests/compat,
# stream): every suite shares the process-global resources (type compiler,
# arenas, caches) with every other suite, in a randomized order, so
# cross-module interference and order dependence surface. The generated
# package lives in the scripts/cmd/alltests nested module (its go.mod
# replaces github.com/velox-io/json with the repo root). make test-shuffle
# reruns this with more rounds or a fixed seed.
(cd scripts/cmd/alltests && go run .)
go -C scripts/cmd/alltests test -count=1 -race -shuffle=on ./testdata/alltests/

# The benchmark module pulls go-json-experiment snapshots that require a newer
# toolchain than the library itself. Probe it first: when the local toolchain
# is too old for its go.mod, skip the module; any other build error still fails.
bm_probe="$(go list -C ./benchmark ./... 2>&1)" && bm_ok=1 || bm_ok=0
if [ "$bm_ok" -eq 0 ]; then
    case "$bm_probe" in
    *"go.mod requires go"*)
        echo "skip: ./benchmark needs a newer toolchain ($(go env GOVERSION))"
        ;;
    *)
        echo "$bm_probe"
        exit 1
        ;;
    esac
else
    go test -C ./benchmark . -count=1
    go test -C ./benchmark -race . -count=1
    go test -C ./benchmark -tags vj_noencvm . -count=1
    go test -C ./benchmark -tags vj_noencvm -race . -count=1
fi

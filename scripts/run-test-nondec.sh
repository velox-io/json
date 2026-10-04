#!/usr/bin/env bash

# Pure-Go decoder round: every package with the native decoder blob excluded,
# so the Go engine (internal/gbind) drives every parse. The engine is plain Go,
# so one OS/arch covers it; CI runs it on Linux AMD64 per Go version instead of
# on every platform job. The race leg over decode/bind runs past the 10m
# default (about 11m locally), so it names its own timeout.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

cd "$REPO_ROOT"

go test -tags vj_nondec ./... -count=1
go test -race -tags vj_nondec ./decode/bind/ ./stream ./tests/ ./tests/compat/ -count=1 -timeout 25m

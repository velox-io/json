#!/usr/bin/env bash

# Pure-Go decoder round: every package with the native decoder blob excluded,
# so the Go engine (internal/gbind) drives every parse. The engine is plain Go,
# so one OS/arch covers it; CI runs it on Linux AMD64 per Go version instead of
# on every platform job. Its decode/bind run reaches the 10m default on a CI
# runner in both legs, so each names its own timeout.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

cd "$REPO_ROOT"

go test -tags vj_nondec ./... -count=1 -timeout 30m
go test -race -tags vj_nondec ./decode/bind/ ./stream ./tests/ ./tests/compat/ -count=1 -timeout 40m

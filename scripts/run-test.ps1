#!/usr/bin/env pwsh

$ErrorActionPreference = "Stop"

$RepoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)

Set-Location $RepoRoot

go test . -count=1
go test -race . -count=1

go test ./vdec -count=1
go test -race ./vdec -count=1

go test ./venc -count=1
go test -race ./venc -count=1
go test -tags vj_noencvm ./venc -count=1
go test -race -tags vj_noencvm ./venc -count=1
go test -tags vjgcstress ./venc -count=1
go test -race -tags vjgcstress ./venc -count=1

go test ./tests/ -count=1
go test -race ./tests/ -count=1
go test -tags vj_noencvm ./tests -count=1
go test -tags vj_noencvm -race ./tests -count=1
go test -tags vjgcstress ./tests -count=1
go test -race -tags vjgcstress ./tests -count=1

go test ./ndec/... -count=1
go test -race ./ndec/... -count=1

go test -C ./benchmark . -count=1
go test -C ./benchmark -race . -count=1
go test -C ./benchmark -tags vj_noencvm . -count=1
go test -C ./benchmark -tags vj_noencvm -race . -count=1

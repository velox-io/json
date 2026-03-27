#!/usr/bin/env pwsh

$ErrorActionPreference = "Stop"

$RepoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)

Set-Location $RepoRoot

go test .
go test -race .

go test ./vdec
go test -race ./vdec

go test ./venc
go test -race ./venc
go test -tags vj_noencvm ./venc
go test -race -tags vj_noencvm ./venc

go test ./tests/
go test -race ./tests/

go test ./ndec/...
go test -race ./ndec/...

go test -C ./benchmark .
go test -C ./benchmark -race .
go test -C ./benchmark -tags vj_noencvm .
go test -C ./benchmark -tags vj_noencvm -race .

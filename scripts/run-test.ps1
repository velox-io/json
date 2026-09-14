#!/usr/bin/env pwsh

$ErrorActionPreference = "Stop"

$RepoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)

Set-Location $RepoRoot

$failed = $false

# `go test -race` exists on windows/amd64 only, never on windows/arm64, where
# it fails before running anything. Decide once, then have every race leg
# degrade to skipped instead of failing or pointlessly duplicating the plain
# leg; Linux and macOS runners keep their -race coverage through run-test.sh.
$RaceArgs = @()
if ((& go env GOARCH) -eq "amd64") {
    $RaceArgs = @("-race")
} else {
    Write-Host "skip: race detector unsupported on $(go env GOOS)/$(go env GOARCH); running without -race" -ForegroundColor Yellow
}

# Runs the plain leg, then the same leg under -race when available. It takes
# no declared parameters on purpose: unbound arguments (including flag-looking
# ones like -tags) accumulate in the automatic $args variable, which splats
# verbatim onto the native go command. A declared parameter named $Args would
# shadow that mechanism and silently swallow tokens during binding. The race
# flags are appended after the arguments because go requires -C, when present,
# to be the first flag on the command line.
function Invoke-GoTest {
    & go test @args
    if ($LASTEXITCODE -ne 0) {
        $script:failed = $true
        Write-Host "FAIL: go test $($args -join ' ')" -ForegroundColor Red
    }
    if ($RaceArgs.Count -gt 0) {
        & go test @args @RaceArgs
        if ($LASTEXITCODE -ne 0) {
            $script:failed = $true
            Write-Host "FAIL: go test $($args -join ' ') $($RaceArgs -join ' ')" -ForegroundColor Red
        }
    }
}

Invoke-GoTest . -count=1

Invoke-GoTest ./vbind ./decode/bind/ -count=1

Invoke-GoTest ./venc -count=1
Invoke-GoTest -tags vj_noencvm ./venc -count=1
Invoke-GoTest -tags vjgcstress ./venc -count=1
Invoke-GoTest -tags vjstress ./venc -count=1

Invoke-GoTest ./tests/ -count=1
Invoke-GoTest -tags vj_noencvm ./tests/ -count=1
Invoke-GoTest -tags vjgcstress ./tests/ -count=1

# Windows has no gc-stress leg, so the encoder VM preemption stress runs
# here directly; Linux and macOS cover it through scripts/gc-stress.sh.
Invoke-GoTest -tags vjgcstress ./tests/preemptstress -count=1

Invoke-GoTest ./tests/compat/ -count=1

# Invoke-GoTest ./ndec/... -count=1

# The benchmark module pulls go-json-experiment snapshots that require a newer
# toolchain than the library itself. Probe it first: when the local toolchain
# is too old for its go.mod, skip the module; any other build error still fails.
$bmProbe = & go list -C ./benchmark ./... 2>&1
if ($LASTEXITCODE -eq 0) {
    Invoke-GoTest -C ./benchmark . -count=1
    Invoke-GoTest -C ./benchmark -tags vj_noencvm . -count=1
} elseif ("$bmProbe" -match "go.mod requires go") {
    Write-Host "skip: ./benchmark needs a newer toolchain ($(go env GOVERSION))"
} else {
    Write-Host $bmProbe
    exit 1
}

if ($failed) {
    Write-Host "`nOne or more test runs failed." -ForegroundColor Red
    exit 1
}

Write-Host "`nAll tests passed." -ForegroundColor Green

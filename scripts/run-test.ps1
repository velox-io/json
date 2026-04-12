#!/usr/bin/env pwsh

$ErrorActionPreference = "Stop"

$RepoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)

Set-Location $RepoRoot

$failed = $false

function Invoke-GoTest {
    param([string[]]$Args)
    & go test @Args
    if ($LASTEXITCODE -ne 0) {
        $script:failed = $true
        Write-Host "FAIL: go test $($Args -join ' ')" -ForegroundColor Red
    }
}

Invoke-GoTest . -count=1
Invoke-GoTest -race . -count=1

Invoke-GoTest ./vdec -count=1
Invoke-GoTest -race ./vdec -count=1

Invoke-GoTest ./venc -count=1
Invoke-GoTest -race ./venc -count=1
Invoke-GoTest -tags vj_noencvm ./venc -count=1
Invoke-GoTest -race -tags vj_noencvm ./venc -count=1
Invoke-GoTest -tags vjgcstress ./venc -count=1
Invoke-GoTest -race -tags vjgcstress ./venc -count=1

Invoke-GoTest ./tests/ -count=1
Invoke-GoTest -race ./tests/ -count=1
Invoke-GoTest -tags vj_noencvm ./tests -count=1
Invoke-GoTest -tags vj_noencvm -race ./tests -count=1
Invoke-GoTest -tags vjgcstress ./tests -count=1
Invoke-GoTest -race -tags vjgcstress ./tests -count=1

# Invoke-GoTest ./ndec/... -count=1
# Invoke-GoTest -race ./ndec/... -count=1

Invoke-GoTest -C ./benchmark . -count=1
Invoke-GoTest -C ./benchmark -race . -count=1
Invoke-GoTest -C ./benchmark -tags vj_noencvm . -count=1
Invoke-GoTest -C ./benchmark -tags vj_noencvm -race . -count=1

if ($failed) {
    Write-Host "`nOne or more test runs failed." -ForegroundColor Red
    exit 1
}

Write-Host "`nAll tests passed." -ForegroundColor Green

# Windows smoke test
# Run from the repository root in PowerShell:
#   powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\smoke-windows.ps1

$ErrorActionPreference = "Stop"
Set-Location (Split-Path -Parent $PSScriptRoot)

$exe = Join-Path $PWD "dist\nlwproxy.exe"
$config = Join-Path $PWD "nlwproxy.json"
$launcher = Join-Path $PWD "start-nlwproxy.cmd"

if (-not (Test-Path $exe)) {
    throw "Missing $exe. Build it with: go build -trimpath -ldflags='-s -w' -o .\dist\nlwproxy.exe .\cmd\nlwproxy"
}
if (-not (Test-Path $config)) { throw "Missing $config" }
if (-not (Test-Path $launcher)) { throw "Missing $launcher" }

& $exe config check --config $config
if ($LASTEXITCODE -ne 0) { throw "config check failed ($LASTEXITCODE)" }

$dashboard = & $exe dashboard --config $config
if ($LASTEXITCODE -ne 0) { throw "dashboard smoke failed ($LASTEXITCODE)" }
if (($dashboard -join "`n") -notmatch "NLWPROXY") { throw "dashboard output did not contain NLWPROXY" }

$launcherText = Get-Content -Raw $launcher
if ($launcherText -notmatch 'console\s+--config') { throw "launcher does not open the console command" }
if ($launcherText -match '(?i)reg\s+query') { throw "launcher must not query the registry" }
if ($launcherText -match '(?i)(sk-[A-Za-z0-9_-]{8,}|LOCAL_TOKEN\s*=\s*[^%\r\n]+)') {
    throw "launcher appears to contain a secret"
}

$help = (& $exe --help 2>&1) -join "`n"
if ($help -notmatch '(?m)^\s*nlwproxy console \[--config path\]') {
    throw "this binary does not expose 'nlwproxy console --config'. Rebuild after the console CLI integration lands"
}

Write-Host "PASS: config, dashboard, console command, and Windows launcher smoke checks passed."

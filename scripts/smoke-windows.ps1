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
$dashboardText = $dashboard -join "`n"
if ($dashboardText -notmatch "NLWPROXY") { throw "dashboard output did not contain NLWPROXY" }
$help = (& $exe --help 2>&1) -join "`n"
$selectionSafeControls = Get-Content -Raw (Join-Path $PWD "README.md")
foreach ($control in @('B', 'U', 'A', 'C', 'F', 'R')) {
    if ($selectionSafeControls -notmatch "(?m)^\|?\s*`?$control`?\s*\|" -and $selectionSafeControls -notmatch "\[$control\]") {
        throw "documentation did not advertise selection-safe control: $control"
    }
}

$launcherText = Get-Content -Raw $launcher
if ($launcherText -notmatch 'console\s+--config') { throw "launcher does not open the console command" }
if ($launcherText -notmatch '--profiles-dir') { throw "launcher does not pin the repository-local profile store" }
if ($launcherText -match '(?i)reg\s+query') { throw "launcher must not query the registry" }
if ($launcherText -match '(?i)(sk-[A-Za-z0-9_-]{8,}|LOCAL_TOKEN\s*=\s*[^%\r\n]+)') {
    throw "launcher appears to contain a secret"
}

$help = (& $exe --help 2>&1) -join "`n"
if ($help -notmatch '(?m)^\s*nlwproxy console \[--config path\]') {
    throw "this binary does not expose 'nlwproxy console --config'. Rebuild after the console CLI integration lands"
}
if ($help -notmatch '(?m)^\s*nlwproxy profile ') { throw "this binary does not expose profile management" }

$profileHelp = (& $exe profile 2>&1) -join "`n"
if ($profileHelp -notmatch 'list\|show\|create\|update\|delete\|use') { throw "profile actions are incomplete" }

# Onboarding itself is covered by injected-store Go tests so smoke never writes
# real HKCU credentials or contacts a live provider. Cross-compile validates the
# Windows registry implementation; the test suite validates six-step setup,
# process-env injection, and existing-profile wizard skip behavior.
go test ./internal/console ./internal/cli -run 'Test(ProfileEnvironmentNames|OnboardingRun|CreateOnboardingProfile|PrepareConsoleProfile)' -count=1
if ($LASTEXITCODE -ne 0) { throw "onboarding tests failed ($LASTEXITCODE)" }
$env:GOOS = 'windows'; $env:GOARCH = 'amd64'
go test ./internal/cli -run '^$'
$crossExit = $LASTEXITCODE
Remove-Item Env:GOOS; Remove-Item Env:GOARCH
if ($crossExit -ne 0) { throw "Windows registry compile smoke failed ($crossExit)" }

Write-Host "PASS: config, dashboard, console command, and Windows launcher smoke checks passed."

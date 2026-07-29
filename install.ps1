$ErrorActionPreference = 'Stop'

Write-Host 'NLW Proxy installer' -ForegroundColor Cyan

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Go 1.22+ is required. Install Go from https://go.dev/dl/ and reopen PowerShell.'
}

$repo = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $repo

Write-Host '[1/3] Building nlwproxy.exe...' -ForegroundColor DarkGray
go build -trimpath -ldflags '-s -w' -o nlwproxy.exe ./cmd/nlwproxy
if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }

Write-Host '[2/3] Installing binary and user PATH...' -ForegroundColor DarkGray
& "$repo\nlwproxy.exe" install
if ($LASTEXITCODE -ne 0) { throw 'NLW Proxy install failed.' }

# Seed the user home with the generic config and example proxy directory.
$homeDir = if ($env:NLWPROXY_HOME) { $env:NLWPROXY_HOME } else { Join-Path $env:APPDATA 'nlwproxy' }
$proxyDir = Join-Path $homeDir 'data\proxies'
New-Item -ItemType Directory -Force -Path $proxyDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $homeDir 'profiles') | Out-Null
$configPath = Join-Path $homeDir 'config.json'
if (-not (Test-Path $configPath)) {
    Copy-Item (Join-Path $repo 'nlwproxy.example.json') $configPath
}

Write-Host '[3/3] Installation complete.' -ForegroundColor Green
Write-Host "Config:  $configPath"
Write-Host "Proxies: $proxyDir"
Write-Host ''
Write-Host 'Before first run, set your credentials:' -ForegroundColor Yellow
Write-Host '  setx MYPROVIDER_API_KEY "your-provider-api-key"'
Write-Host '  setx NLW_PROXY_LOCAL_TOKEN "choose-a-local-gateway-token"'
Write-Host ''
Write-Host 'Then open a NEW terminal and run: nlwproxy' -ForegroundColor Cyan

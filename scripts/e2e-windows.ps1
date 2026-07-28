# End-to-end Windows validation for the TUI-managed gateway.
# Uses persisted environment credentials without printing them.
[CmdletBinding()]
param(
    [switch]$SkipOMP,
    [int]$StartupTimeoutSeconds = 30,
    [int]$ShutdownTimeoutSeconds = 15
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

$exe = Join-Path $repoRoot "dist\nlwproxy.exe"
$config = Join-Path $repoRoot "nlwproxy.json"
$base = "http://127.0.0.1:8787"
$port = 8787
$process = $null

function Get-PersistedEnvironmentValue([string]$Name) {
    $value = [Environment]::GetEnvironmentVariable($Name, "Process")
    if (-not $value) { $value = [Environment]::GetEnvironmentVariable($Name, "User") }
    return $value
}

function Wait-Until([scriptblock]$Check, [int]$TimeoutSeconds, [string]$Failure) {
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        if (& $Check) { return }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $deadline)
    throw $Failure
}

function Test-ListeningPort([int]$Port) {
    $client = [Net.Sockets.TcpClient]::new()
    try {
        $task = $client.ConnectAsync("127.0.0.1", $Port)
        return $task.Wait(300) -and $client.Connected
    } catch { return $false } finally { $client.Dispose() }
}

if (-not (Test-Path $exe)) { throw "Missing $exe. Build the Windows executable first." }
if (-not (Test-Path $config)) { throw "Missing $config" }
if (Test-ListeningPort $port) { throw "Port $port is already in use; refusing to test an unrelated process." }

$cfg = Get-Content -Raw $config | ConvertFrom-Json
$localTokenName = $cfg.server.local_token_env
$provider = @($cfg.upstreams | Where-Object { $_.enabled -and $_.name -eq "ReffaUnlimited" })[0]
if (-not $provider) { throw "Enabled ReffaUnlimited provider not found in config." }
$localToken = Get-PersistedEnvironmentValue $localTokenName
$providerKey = Get-PersistedEnvironmentValue $provider.api_key_env
if (-not $localToken) { throw "Missing local gateway credential in $localTokenName (process or HKCU environment)." }
if (-not $providerKey) { throw "Missing provider credential in $($provider.api_key_env) (process or HKCU environment)." }
[Environment]::SetEnvironmentVariable($localTokenName, $localToken, "Process")
[Environment]::SetEnvironmentVariable($provider.api_key_env, $providerKey, "Process")

try {
    # Redirected stdin gives the TUI a controlled non-interactive input stream.
    # Closing it after readiness exercises the same graceful TUI shutdown path as Q.
    $psi = [Diagnostics.ProcessStartInfo]::new()
    $psi.FileName = $exe
    $psi.Arguments = 'console --config "' + $config + '" --profiles-dir "' + (Join-Path $repoRoot "profiles") + '"'
    $psi.UseShellExecute = $false
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $false
    $psi.RedirectStandardError = $false
    $psi.CreateNoWindow = $true
    $process = [Diagnostics.Process]::Start($psi)

    Wait-Until {
        if ($process.HasExited) {
            throw "TUI exited before readiness (code $($process.ExitCode))."
        }
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri "$base/health" -TimeoutSec 2
            return $response.StatusCode -eq 200
        } catch { return $false }
    } $StartupTimeoutSeconds "Gateway did not become ready at $base/health."
    Write-Host "PASS readiness: $base/health"

    $headers = @{ Authorization = "Bearer $localToken" }
    $models = Invoke-RestMethod -Headers $headers -Uri "$base/v1/models" -TimeoutSec 20
    if (-not $models.data -or @($models.data).Count -lt 1) { throw "Authenticated /v1/models returned no models." }
    Write-Host "PASS authenticated models: $(@($models.data).Count) model(s)"

    if (-not $SkipOMP) {
        # OMP/OpenAI-compatible connectivity is proven through the configured
        # ReffaUnlimited route. No key or response content is logged.
        $providerModels = Invoke-RestMethod -Headers @{ Authorization = "Bearer $providerKey" } -Uri ($provider.base_url.TrimEnd('/') + "/models") -TimeoutSec 20
        if (-not $providerModels.data -or @($providerModels.data).Count -lt 1) { throw "OMP ReffaUnlimited /models returned no models." }
        Write-Host "PASS OMP ReffaUnlimited connectivity: $(@($providerModels.data).Count) model(s)"
    }

    $process.StandardInput.WriteLine("q")
    $process.StandardInput.Close()
    if (-not $process.WaitForExit($ShutdownTimeoutSeconds * 1000)) { throw "TUI did not exit within $ShutdownTimeoutSeconds seconds." }
    if ($process.ExitCode -ne 0) { throw "TUI exited with code $($process.ExitCode)." }
    Wait-Until { -not (Test-ListeningPort $port) } $ShutdownTimeoutSeconds "Port $port remained open after graceful close."
    Write-Host "PASS shutdown: TUI exited cleanly and port $port was released"
} finally {
    if ($process -and -not $process.HasExited) {
        try { $process.Kill($true); $process.WaitForExit(5000) | Out-Null } catch { }
    }
}

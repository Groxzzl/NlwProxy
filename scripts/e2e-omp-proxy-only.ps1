# Deterministic OMP proxy-only contract test using a local mock HTTP proxy.
[CmdletBinding()]
param([int]$ProxyPort = 18787)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$mock = Join-Path $env:TEMP "nlwproxy-mock-proxy-$PID.py"
$log = Join-Path $env:TEMP "nlwproxy-mock-proxy-$PID.log"
$process = $null
@'
import http.server, json, sys
port, log = int(sys.argv[1]), sys.argv[2]
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        with open(log, "w", encoding="utf-8") as f: f.write(self.path)
        payload=json.dumps({"data":[{"id":"mock/omp"}],"object":"list"}).encode()
        self.send_response(200); self.send_header("Content-Type","application/json")
        self.send_header("Content-Length",str(len(payload))); self.end_headers(); self.wfile.write(payload)
    def log_message(self, *_): pass
http.server.ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
'@ | Set-Content -Encoding UTF8 $mock
try {
    $process = Start-Process python -ArgumentList @($mock, $ProxyPort, $log) -PassThru -WindowStyle Hidden
    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    do {
        try { $ready = Test-NetConnection 127.0.0.1 -Port $ProxyPort -InformationLevel Quiet -WarningAction SilentlyContinue } catch { $ready = $false }
        if (-not $ready) { Start-Sleep -Milliseconds 100 }
    } while (-not $ready -and [DateTime]::UtcNow -lt $deadline)
    if (-not $ready) { throw "Mock proxy failed to start." }

    $response = Invoke-RestMethod -Proxy "http://127.0.0.1:$ProxyPort" -Uri "http://omp.invalid/v1/models" -TimeoutSec 5
    if (@($response.data).Count -ne 1 -or $response.data[0].id -ne "mock/omp") { throw "OMP model request failed through mock proxy." }
    if ((Get-Content -Raw $log) -ne "http://omp.invalid/v1/models") { throw "Mock proxy did not observe the absolute OMP URL." }
    Write-Host "PASS OMP request used mock proxy"

    try {
        Invoke-WebRequest -UseBasicParsing -Uri "http://omp.invalid/v1/models" -TimeoutSec 2 | Out-Null
        throw "Direct OMP request unexpectedly succeeded."
    } catch {
        if ($_.Exception.Message -eq "Direct OMP request unexpectedly succeeded.") { throw }
    }
    Write-Host "PASS direct OMP path blocked"
} finally {
    if ($process -and -not $process.HasExited) { Stop-Process -Id $process.Id -Force }
    Remove-Item $mock, $log -Force -ErrorAction SilentlyContinue
}

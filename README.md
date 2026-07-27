# NlwProxy

NlwProxy is a loopback-only, OpenCode-focused gateway and route manager. It keeps approved network routes in one strict configuration, stores provider secrets in environment variables, and exposes a professional ANSI operations dashboard.

> **Safety scope:** legitimate connectivity and resilience only. NlwProxy does not harvest public proxies, rotate identities to evade quotas, replay `401`/`403`/`429` responses through another IP, or bypass provider controls.

## Current product surface

- Strict JSON configuration with loopback-only listener validation.
- Direct, authorized HTTP(S), and SOCKS5/SOCKS5H route definitions.
- Proxy lifecycle CLI: add, edit, remove, list, enable, disable, and TCP-test.
- Route strategy and priority management.
- ANSI dashboard with color-independent symbols and plain-output fallback.
- Console V2 route snapshots with active/total/error counts, mean latency, input/output tokens, last-used time, circuit state, and direct/HTTP/SOCKS5 transport identity.
- Optional cached exit-IP diagnostics per route. Probes are explicit diagnostics, use the route transport, and never run on the request path.
- OpenCode provider sample and free legitimate route examples.

Gateway availability depends on the gateway module in this repository. The dashboard reports configured state when the service is not running.

## Build and verify

Requires Go 1.22+.

```sh
go test ./...
go test -race ./...
go vet ./...
go build -trimpath -ldflags="-s -w" -o dist/nlwproxy.exe ./cmd/nlwproxy
```

Windows PowerShell:

```powershell
go test ./...
go build -trimpath -ldflags="-s -w" -o dist\nlwproxy.exe .\cmd\nlwproxy
```

## Three-minute setup

### Windows PowerShell

From the repository directory, launch the interactive console with:

```powershell
.\start-nlwproxy.cmd
```

The launcher uses `dist\nlwproxy.exe` and the repository's `nlwproxy.json`, so it also works when the directory path contains spaces. It does not read the registry or store credentials. Set authorized provider credentials in the same PowerShell session before launching when the console starts the gateway:

```powershell
$env:OPENCODE_API_KEY = 'your-authorized-provider-key'
$env:NLW_PROXY_LOCAL_TOKEN = 'generate-a-long-random-local-token'
.\start-nlwproxy.cmd
```

Use `launch-nlwproxy.cmd` only when you want a separate Windows console window. Use `start-nlwproxy.cmd` for normal PowerShell onboarding so errors remain visible.

### Route observability and exit-IP diagnostics

`GET /health` exposes dashboard-facing route snapshots. Each route includes `transport`, `active`, `total`, `errors`, `latency_ns`, `input_tokens`, `output_tokens`, `last_used`, `health`, and `circuit`. Token counts come only from provider usage metadata; prompts and responses are not retained.

Exit-IP checks are disabled by default. Enable a bounded HTTPS probe in JSON when operational diagnostics require it:

```json
"observability": {
  "exit_ip_probe": {
    "enabled": true,
    "url": "https://api.ipify.org?format=json",
    "timeout": "3s",
    "cache_ttl": "15m"
  }
}
```

The probe is cached per route and must be invoked as a diagnostic; it is never performed per model request. The configured service learns the route's public IP, so leave this disabled unless that disclosure is acceptable.

Console screens:

1. **Connections** — list configured providers/routes and enable, disable, edit, or remove one.
2. **Provider editor** — enter the provider base URL, API-key environment-variable name, route priority, and optional authorized proxy URL. Secret values belong in environment variables, never in JSON.
3. **Test/health** — run the route TCP test and inspect gateway diagnostics. A TCP pass does not prove provider authorization.
4. **Models** — discover models through the running local gateway, then select an identifier authorized for your provider account.

The approved console command is `nlwproxy console --config <path>`. `start-nlwproxy.cmd` supplies the repository-relative executable and config paths automatically.

```sh
# 1. Create a configuration.
nlwproxy init --config ./nlwproxy.json

# 2. Add an authorized provider route. This stores only the env-var name.
nlwproxy proxy add direct \
  --base-url https://api.openai.com/v1 \
  --api-key-env OPENAI_API_KEY \
  --priority 10 \
  --config ./nlwproxy.json

# 3. Validate and inspect.
nlwproxy config check --config ./nlwproxy.json
nlwproxy proxy list --config ./nlwproxy.json
nlwproxy dashboard --config ./nlwproxy.json
```

Set provider credentials in the shell that launches NlwProxy:

```sh
export OPENAI_API_KEY='your-authorized-provider-key'
export NLW_PROXY_LOCAL_TOKEN='generate-a-long-random-local-token'
```

PowerShell:

```powershell
$env:OPENAI_API_KEY = 'your-authorized-provider-key'
$env:NLW_PROXY_LOCAL_TOKEN = 'generate-a-long-random-local-token'
```

### Provider editing, testing, and model discovery

Use the interactive console for routine changes. The equivalent non-interactive commands are:

```powershell
# Change provider details; omitted flags retain their existing values.
.\dist\nlwproxy.exe proxy edit opencode --base-url https://api.example.com/v1 --api-key-env PROVIDER_API_KEY --priority 10 --config .\nlwproxy.json

# Test network reachability only.
.\dist\nlwproxy.exe proxy test opencode --timeout 5s --config .\nlwproxy.json

# Validate and inspect without printing secret values.
.\dist\nlwproxy.exe config check --config .\nlwproxy.json
.\dist\nlwproxy.exe config print-redacted --config .\nlwproxy.json
```

With the gateway running, model discovery uses the local OpenAI-compatible endpoint. Pass the local token as an environment variable; do not paste it into documentation or source files:

```powershell
$headers = @{ Authorization = "Bearer $env:NLW_PROXY_LOCAL_TOKEN" }
Invoke-RestMethod -Headers $headers http://127.0.0.1:8787/v1/models
```

The returned ID must match the OpenCode model entry. Discovery reports the local route alias; provider entitlement still controls which upstream model requests succeed.

Default config: `%AppData%\nlwproxy\config.json` on Windows and `$XDG_CONFIG_HOME/nlwproxy/config.json` elsewhere. Override with `NLWPROXY_CONFIG` or `--config`.

## OpenCode integration

Merge [`examples/opencode.jsonc`](examples/opencode.jsonc) into the installed OpenCode configuration; preserve unrelated providers and settings. The entry uses:

```text
baseURL: http://127.0.0.1:8787/v1
apiKey:  {env:NLW_PROXY_LOCAL_TOKEN}
model:   nlwproxy/opencode-route
```

The configured model alias must match what the running gateway exposes. See [`docs/opencode.md`](docs/opencode.md) for configuration locations, merge steps, and diagnostics.

## CLI reference

```text
nlwproxy init [--config path] [--force]
nlwproxy config check [--config path]
nlwproxy config path [--config path]
nlwproxy config print-redacted [--config path]

nlwproxy proxy add <name> --base-url <https-url>
  [--proxy-url <http|https|socks5|socks5h://host:port>]
  [--api-key-env ENV] [--priority N] [--weight N]
  [--enabled=true|false] [--config path]
nlwproxy proxy edit <name> [route flags]
nlwproxy proxy remove <name> [--config path]
nlwproxy proxy enable <name> [--config path]
nlwproxy proxy disable <name> [--config path]
nlwproxy proxy list [--config path]
nlwproxy proxy test <name> [--timeout 5s] [--config path]

nlwproxy route status [--config path]
nlwproxy route set-strategy <round_robin|failover> [--config path]
nlwproxy route set-priority <name> <number> [--config path]
nlwproxy setup [--opencode-config path] [--dry-run|--rollback]
nlwproxy uninstall [--opencode-config path]
nlwproxy serve [--config path]
nlwproxy status [--config path]
nlwproxy dashboard [--config path]
nlwproxy console [--config path]
nlwproxy version
```

`proxy test` performs a TCP reachability check against the proxy endpoint, or the upstream for direct routes. It does not send a model request or validate provider authorization.

Set `NO_COLOR=1` to disable ANSI colors. Redirected output automatically stays plain. Dashboard states remain readable by symbol: `● HEALTHY`, `◐ DEGRADED`, `○ OPEN`, `× AUTH_REQUIRED`, `— DISABLED`.

## Legitimate free route options

| Route | Endpoint example | Notes |
|---|---|---|
| Direct | no `proxy_url` | Fastest default; no extra software. |
| Tor local SOCKS5 | `socks5h://127.0.0.1:9050` | Slower; provider policy still applies. |
| SSH dynamic forwarding | `socks5h://127.0.0.1:1080` | Requires a host you own or may use. |
| User-authorized WARP local proxy | client-defined loopback port | Configure only when your WARP setup exposes a documented endpoint. |

Executable JSON samples:

- [`examples/nlwproxy.direct.json`](examples/nlwproxy.direct.json)
- [`examples/nlwproxy.tor.json`](examples/nlwproxy.tor.json)
- [`examples/nlwproxy.ssh.json`](examples/nlwproxy.ssh.json)
- [`examples/nlwproxy.warp-local.json`](examples/nlwproxy.warp-local.json)

See [`docs/routes.md`](docs/routes.md). The YAML file is documentation-only; the current strict loader accepts JSON.

## Security

- Listener configuration must use loopback.
- Provider key values stay in named environment variables.
- Plaintext credentials in `base_url` and `proxy_url` are rejected.
- HTTPS is required for upstream provider URLs.
- Proxy and upstream URLs reject CRLF characters.
- Configuration writes use private permissions and replacement via a temporary file.
- Never expose the local endpoint publicly or use unknown public proxies.
- Route fallback is for transport resilience only. Authentication, authorization, quota, and `429` responses are returned without retrying or switching routes.

## Troubleshooting

| Symptom | Check |
|---|---|
| `configuration invalid` | Run `nlwproxy config check --config <path>` and correct the exact field reported. |
| `proxy not found` | Run `nlwproxy proxy list`; route names are case-sensitive. |
| TCP test fails | Start Tor/SSH/WARP local proxy, verify its loopback port, then retry. |
| OpenCode cannot connect | Confirm gateway is running on the configured listener and `baseURL` ends in `/v1`. |
| Provider returns `401`/`403` | Check the provider key environment variable. Do not fail over to evade authorization. |
| Provider returns `429` | Honor provider limits. Do not rotate routes to obtain more quota. |
| Garbled terminal colors | Set `NO_COLOR=1`; use Windows Terminal or a current console. |
| Wrong config loaded | Run `nlwproxy config path`; check `NLWPROXY_CONFIG` and `--config`. |

## Windows smoke workflow

Run the automated launcher/config/dashboard checks from PowerShell:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\smoke-windows.ps1
```

Then run the interactive check:

```powershell
.\start-nlwproxy.cmd
```

Confirm the console opens, the provider list renders, model discovery gives a clear result, and `Q` exits with no secret printed. The automated script intentionally does not start the interactive console or contact an external provider.

## Architecture

The source diagram is preserved at [`docs/Nlw_Proxy.excalidraw`](docs/Nlw_Proxy.excalidraw). Product-surface code lives in `internal/cli` and `internal/tui` and does not alter gateway routing semantics.

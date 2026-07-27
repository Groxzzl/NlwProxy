# OpenCode integration

NlwProxy presents an OpenAI-compatible local endpoint to OpenCode. Keep both processes on the same workstation and keep the listener on loopback.

## 1. Locate OpenCode configuration

OpenCode commonly reads a project-level `opencode.json`/`opencode.jsonc` or a user configuration under its platform config directory. Confirm the active location against your installed OpenCode version. Never replace the entire file if it already contains providers, agents, or tools.

## 2. Merge the provider

Copy the `provider.nlwproxy` object and optional top-level `model` value from [`../examples/opencode.jsonc`](../examples/opencode.jsonc). Preserve unrelated keys.

```jsonc
{
  "provider": {
    "nlwproxy": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "NLW Proxy (local)",
      "options": {
        "baseURL": "http://127.0.0.1:8787/v1",
        "apiKey": "{env:NLW_PROXY_LOCAL_TOKEN}"
      },
      "models": {
        "opencode-route": {
          "name": "NLW Managed Route"
        }
      }
    }
  },
  "model": "nlwproxy/opencode-route"
}
```

`NLW_PROXY_LOCAL_TOKEN` authenticates OpenCode to the local gateway. Provider credentials remain separate, e.g. `OPENAI_API_KEY`, and are referenced by `api_key_env` in NlwProxy configuration.

## 3. Prepare the route

On Windows, launch the repository console first:

```powershell
.\start-nlwproxy.cmd
```

Open **Connections**, add or edit the provider, and set:

- Base URL: the authorized provider's HTTPS OpenAI-compatible `/v1` URL.
- API-key environment variable: a name such as `OPENAI_API_KEY`, never the key value.
- Priority and optional proxy URL: only routes you own or are authorized to use.

Use the console's **Test** action for TCP reachability. It does not validate credentials, model access, or quota.

```sh
nlwproxy init --config ./nlwproxy.json
nlwproxy proxy add direct --base-url https://api.openai.com/v1 --api-key-env OPENAI_API_KEY --priority 10 --config ./nlwproxy.json
nlwproxy config check --config ./nlwproxy.json
```

Use a provider base URL and model alias that your account is authorized to access. The sample identifiers are examples, not free credentials or guaranteed entitlement.

## 4. Start and verify

Start the repository's gateway command, then verify its health endpoint and open OpenCode:

```sh
curl http://127.0.0.1:8787/health
opencode
```

If strict client validation is active, normal browser/curl requests to model endpoints may be rejected by design. Use the health endpoint for basic liveness and OpenCode for an end-to-end test.

### Discover the local model alias

PowerShell:

```powershell
$headers = @{ Authorization = "Bearer $env:NLW_PROXY_LOCAL_TOKEN" }
(Invoke-RestMethod -Headers $headers http://127.0.0.1:8787/v1/models).data
```

The local gateway currently advertises `opencode-route`; OpenCode addresses it as `nlwproxy/opencode-route`. If the console adds model selection, choose an upstream model your provider account is authorized to use. Model discovery is not a grant of provider entitlement.

### Windows launch and smoke checks

`start-nlwproxy.cmd` directly runs `dist\nlwproxy.exe console --config nlwproxy.json` with paths anchored to the script directory. It does not use `reg query`, persist keys, or print secret values.

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\smoke-windows.ps1
.\start-nlwproxy.cmd
```

The automated script checks config/dashboard output, launcher safety, and whether the built executable exposes the console command. The second command is the manual interactive smoke test.

## Diagnostics

```sh
nlwproxy config path
nlwproxy config check
nlwproxy config print-redacted
nlwproxy proxy list
nlwproxy route status
nlwproxy dashboard
```

- `ECONNREFUSED`: gateway is not listening or the ports differ.
- `401` from local gateway: `NLW_PROXY_LOCAL_TOKEN` differs between processes.
- `401`/`403` from provider: provider key or entitlement is invalid; do not switch IPs to bypass it.
- `429`: wait according to provider policy; NlwProxy must not route-replay quota errors.
- Model not found: align the OpenCode model key with the alias exposed by the gateway/upstream.

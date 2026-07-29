# NLW Proxy

NLW Proxy is a local OpenAI-compatible gateway for OpenCode and other clients that use the OpenAI API shape. It sends upstream requests through a pool of HTTP or SOCKS5 proxies and exposes a terminal dashboard for testing and managing that pool.

The gateway listens on `127.0.0.1:8787` by default. It does not expose itself to the local network unless you change the listen address.

## What it does

- Loads proxy lists from multiple text files.
- Tests proxies before adding them to the active route pool.
- Rotates healthy proxies with round-robin selection.
- Moves a proxy into cooldown after an upstream `429` response.
- Reads `Retry-After` and `retry-after-ms` when the provider includes them.
- Blocks direct upstream connections when `proxy_only` is enabled.
- Shows proxy health, latency, location, requests, models, routes, and logs in the TUI.
- Stores API keys in environment variables rather than config files.

## Requirements

- Go 1.22 or newer
- Windows, Linux, or macOS
- An API key for an OpenAI-compatible provider
- HTTP or SOCKS5 proxies

Authenticated proxies are preferable. Public proxy lists are volatile and often rate-limited already.

## Install

### Windows

Open PowerShell:

```powershell
git clone https://github.com/Groxzzl/NlwProxy.git
cd NlwProxy
.\install.ps1
```

If PowerShell blocks the script:

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

The script builds `nlwproxy.exe`, copies it to `%LOCALAPPDATA%\nlwproxy\bin`, and adds that directory to your user PATH.

### Linux and macOS

```bash
git clone https://github.com/Groxzzl/NlwProxy.git
cd NlwProxy
chmod +x install.sh
./install.sh
```

The script installs the binary under `~/.local/bin`.

### Manual build

```bash
go build -trimpath -o nlwproxy ./cmd/nlwproxy
./nlwproxy install
```

PowerShell requires the executable prefix:

```powershell
go build -trimpath -o nlwproxy.exe ./cmd/nlwproxy
.\nlwproxy.exe install
```

Open a new terminal after installation. Confirm that the command is available:

```powershell
nlwproxy version
```

## First run

The default config expects these environment variables:

```powershell
setx MYPROVIDER_API_KEY "your-provider-api-key"
setx NLW_PROXY_LOCAL_TOKEN "choose-a-local-token"
```

Open a new terminal after running `setx`.

`MYPROVIDER_API_KEY` is sent to the upstream provider. `NLW_PROXY_LOCAL_TOKEN` is the key local clients use when connecting to NLW Proxy.

Start the dashboard:

```powershell
nlwproxy
```

The first run creates the user data directories automatically.

Windows paths:

```text
%APPDATA%\nlwproxy\config.json
%APPDATA%\nlwproxy\profiles\
%APPDATA%\nlwproxy\data\proxies\
```

Linux and macOS paths:

```text
~/.config/nlwproxy/config.json
~/.config/nlwproxy/profiles/
~/.config/nlwproxy/data/proxies/
```

Set `NLWPROXY_HOME` if you want to use another directory.

## Configuration

The installer copies `nlwproxy.example.json` to the user config directory. The default file looks like this:

```json
{
  "client": "opencode",
  "server": {
    "listen": "127.0.0.1:8787",
    "local_token_env": "NLW_PROXY_LOCAL_TOKEN",
    "max_body_bytes": 20971520
  },
  "routing": {
    "strategy": "round_robin"
  },
  "proxy_only": true,
  "observability": {
    "exit_ip_probe": {
      "url": "https://api.ipify.org?format=json",
      "timeout": "3s",
      "cache_ttl": "15m"
    }
  },
  "upstreams": [
    {
      "name": "MyProvider",
      "base_url": "https://opencode.ai/zen/v1",
      "api_key_env": "MYPROVIDER_API_KEY",
      "priority": 10,
      "weight": 1,
      "enabled": true
    }
  ]
}
```

`local_token_env` and `api_key_env` contain environment variable names. Do not put real keys in the JSON file.

| Field | Use |
| --- | --- |
| `server.listen` | Local address and port for the gateway |
| `server.local_token_env` | Environment variable containing the local client token |
| `routing.strategy` | `round_robin` or `failover` |
| `proxy_only` | Blocks direct upstream traffic when set to `true` |
| `upstreams[].base_url` | OpenAI-compatible provider base URL |
| `upstreams[].api_key_env` | Environment variable containing the provider key |
| `upstreams[].enabled` | Enables or disables the upstream |

## Add proxies

The persistent proxy directory is shown on the dashboard and printed by the installer.

Accepted line formats:

```text
host:port
host:port:username:password
http://host:port
http://username:password@host:port
socks5://host:port
```

Example:

```text
203.0.113.10:8080:user1:pass1
203.0.113.11:8080:user1:pass1
socks5://198.51.100.5:1080
```

Blank lines and lines beginning with `#` are ignored.

You can add proxies in either place:

1. Copy one or more `.txt` files into the persistent proxy directory.
2. Open the Proxies page, press `i`, and enter a file path.

Press `t` on the Proxies page to run the health test. Only working proxies are loaded into the runtime route pool.

NLW Proxy does not scrape public lists during normal startup. Set `NLWPROXY_SCRAPE_PUBLIC=1` only if you want the dashboard startup path to refresh `github-auto.txt`. Entries still need to pass the health test before the runtime uses them.

The repository also includes a stricter maintenance tool. It checks TLS connectivity and then requests the OpenCode model catalog through each proxy:

```powershell
go run ./cmd/verifyproxies `
  -out "$env:APPDATA\nlwproxy\data\proxies\verified.txt" `
  -private "$env:APPDATA\nlwproxy\data\proxies\private.txt" `
  -limit 160
```

The runtime tests the resulting file again at startup, sorts the surviving proxies by latency, and activates at most 120 routes.

## Connect OpenCode

NLW Proxy exposes an OpenAI-compatible API at:

```text
http://127.0.0.1:8787/v1
```

Merge this provider into your OpenCode config:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "nlwproxy": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "NLW Proxy",
      "options": {
        "baseURL": "http://127.0.0.1:8787/v1",
        "apiKey": "{env:NLW_PROXY_LOCAL_TOKEN}"
      },
      "models": {
        "opencode-route": {
          "name": "NLW managed route"
        }
      }
    }
  },
  "model": "nlwproxy/opencode-route"
}
```

A copy is available at [`examples/opencode.jsonc`](examples/opencode.jsonc).

Start NLW Proxy before opening a model through OpenCode:

```powershell
nlwproxy
```

## Claude Code compatibility

Claude Code sends requests to Anthropic's `/v1/messages` endpoint. NLW Proxy currently implements OpenAI-compatible `/v1/chat/completions` and `/v1/responses` endpoints, so Claude Code cannot connect directly yet.

Use OpenCode or another OpenAI-compatible client. Native Claude Code support requires an Anthropic request and response adapter.

## OpenAI SDK

Python:

```python
import os
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8787/v1",
    api_key=os.environ["NLW_PROXY_LOCAL_TOKEN"],
)

response = client.chat.completions.create(
    model="opencode-route",
    messages=[{"role": "user", "content": "Say hello"}],
)

print(response.choices[0].message.content)
```

Node.js:

```js
import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "http://127.0.0.1:8787/v1",
  apiKey: process.env.NLW_PROXY_LOCAL_TOKEN,
});

const response = await client.chat.completions.create({
  model: "opencode-route",
  messages: [{ role: "user", content: "Say hello" }],
});

console.log(response.choices[0].message.content);
```

## Dashboard controls

| Key | Action |
| --- | --- |
| `Tab` | Move focus between the sidebar and page |
| Arrow keys | Move through menus and tables |
| `Enter` | Open the selected page or row |
| `Esc` | Return to the sidebar or cancel input |
| `/` | Search the proxy list |
| `i` | Import a proxy file |
| `t` | Test all proxies |
| `f` | Freeze dashboard updates |
| `q` | Quit |
| `Ctrl+Shift+C` | Copy selected terminal text |

The dashboard has pages for overview, models, proxies, routes, requests, logs, profiles, and settings.

## Command reference

```text
nlwproxy
nlwproxy install
nlwproxy init
nlwproxy config
nlwproxy proxy
nlwproxy route
nlwproxy setup
nlwproxy status
nlwproxy serve
nlwproxy console
nlwproxy tui
nlwproxy profile
nlwproxy version
nlwproxy help
```

Run `nlwproxy help` for command arguments.

## Request flow

```text
OpenCode
   |
   v
NLW Proxy on 127.0.0.1:8787
   |
   v
healthy proxy selected by round-robin
   |
   v
OpenAI-compatible provider
```

When the provider returns `429`, NLW Proxy puts that route into cooldown and tries another eligible route. If every route is unavailable, the gateway returns `NO_HEALTHY_PROXY` and includes the earliest known recovery time.

## Project layout

```text
cmd/nlwproxy/             CLI entrypoint
internal/cli/             commands, installer, bootstrap
internal/config/          config loading and validation
internal/gateway/         HTTP gateway
internal/routing/         route selection and circuit state
internal/retry/           retry classification and Retry-After parsing
internal/proxymanager/    proxy import, testing, and state
internal/proxyimport/     public proxy sources and parsers
internal/geo/             exit IP location lookup
internal/runtime/         gateway lifecycle and hot reload
internal/tuiapp/          Bubble Tea application
internal/tuiapp/pages/    dashboard pages
internal/tuiapp/ui/       shared TUI components
examples/                 client and route examples
docs/                     additional notes
data/proxies/             source checkout proxy directory
```

## Development

Run the same checks used by CI:

```bash
gofmt -w .
go vet ./...
go test ./... -count=1 -timeout 180s
go build -trimpath ./cmd/nlwproxy
```

GitHub Actions runs tests on Windows, Linux, and macOS. It also cross-builds Windows AMD64, Linux AMD64, and macOS ARM64 binaries.

## Security

The gateway binds to localhost by default. Keep it that way unless you intend to expose it.

Proxy files, generated profiles, local environment scripts, and build output are ignored by Git. Check `git status` before committing if you add another credential file format.

NLW Proxy can only protect the path between the client and the configured upstream as well as the chosen proxy allows. Do not send sensitive traffic through an untrusted public proxy.

## Troubleshooting

### `nlwproxy` is not recognized

Run the installer from the cloned repository, then open a new terminal:

```powershell
.\install.ps1
```

Confirm the installed command:

```powershell
Get-Command nlwproxy
nlwproxy version
```

### Profile setup required

Update to the latest version and run the installer again. Current builds create the config and profile directories on first run.

### Missing provider credential

The error names the environment variable it expected. Set that variable and open a new terminal.

```powershell
setx MYPROVIDER_API_KEY "your-provider-api-key"
```

### No healthy proxy

Open the Proxies page and press `t`. Add another proxy file if every entry is dead or in cooldown.

### Upstream returns 401 or 403

Check the provider key and the provider's account permissions. These responses do not indicate a proxy health problem.

### Upstream returns 429

The selected route has reached a provider limit. NLW Proxy reads the retry delay when available, moves the route into cooldown, and tries the next eligible route.

### Proxy test appears stuck

Each test has a timeout. Wait for the current batch to finish. Proxies that do not respond are marked dead.

## License

No license file is included yet. Until one is added, normal copyright rules apply.

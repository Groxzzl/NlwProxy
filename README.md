# NLW Proxy

**NLW Proxy** is a local, OpenAI-compatible gateway that sits between your AI coding client (OpenCode or Claude Code) and an upstream provider, and routes every outbound request through a rotating pool of HTTP/SOCKS5 proxies. Point your client at `http://127.0.0.1:8787/v1`, and NLW Proxy transparently forwards each request to your provider over a healthy proxy — round-robin across the pool, with automatic per-proxy cooldown when a proxy gets rate-limited (HTTP 429). It ships with a live Bubble Tea terminal dashboard so you can watch traffic, test proxies, and copy your connection details without leaving the terminal.

## Features

- **Proxy-only routing** — when `proxy_only` is enabled, no request ever leaves your machine without going through a proxy (fail-closed, no accidental direct connections).
- **Round-robin rotation** — outbound requests are spread evenly across all healthy proxies.
- **429 cooldown auto-rotate** — a proxy that returns `429 Too Many Requests` is put on cooldown and skipped; the request is retried on the next healthy proxy automatically.
- **Multi-file proxy loader** — drop any number of `*.txt` proxy files into `data/proxies/`; all are auto-loaded on startup, deduplicated by `host:port`, and merged into one pool.
- **Health testing** — test every proxy on demand and keep only the live ones in rotation.
- **Geo / country per proxy** — each proxy is annotated with its exit IP country so you can see where traffic is coming out.
- **Live TUI dashboard** — real-time pages for overview, proxies, routes, requests, models, logs, and settings.
- **Copy-ready connection details** — the Overview page shows your Base URL and API-key env reference ready to paste into OpenCode / Claude Code.

## Requirements

- **Go 1.22+** — required to build from source.
- **OS** — Windows, macOS, or Linux.
- **A provider API key** — e.g. [OpenCode Zen](https://opencode.ai) (or any OpenAI-compatible upstream). The key is read from an environment variable, never stored in config.
- **Proxies** — a list of HTTP/SOCKS5 proxies, e.g. from [Webshare](https://www.webshare.io). Authenticated `host:port:user:pass` proxies are recommended.

## Install

Clone the repository and run the installer from inside it.

### Windows (PowerShell)

```powershell
git clone https://github.com/Groxzzl/NlwProxy.git
cd NlwProxy
.\install.ps1
```

If PowerShell blocks local scripts, use:

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

### macOS / Linux

```bash
git clone https://github.com/Groxzzl/NlwProxy.git
cd NlwProxy
chmod +x install.sh
./install.sh
```

The installer builds NLW Proxy, installs the binary into your user PATH, and creates the persistent user directories for config, profiles, and proxy files.

Before the first run, set the provider key and the local token that clients will use to authenticate to NLW Proxy:

```powershell
setx MYPROVIDER_API_KEY "your-provider-api-key"
setx NLW_PROXY_LOCAL_TOKEN "choose-a-local-gateway-token"
```

Open a **new terminal** after installation and setting the variables, then run:

```bash
nlwproxy
```

Running `nlwproxy` with no arguments launches the live dashboard from any working directory.

### Manual build

```bash
go build -trimpath -o nlwproxy ./cmd/nlwproxy
./nlwproxy install
```

On Windows, run the manual binary as `.\nlwproxy.exe install`.

## Project Structure

```
NlwProxy/
├── cmd/
│   └── nlwproxy/            # main entrypoint (main.go) — CLI + dashboard bootstrap
├── internal/
│   ├── cli/                 # command parsing and subcommand dispatch
│   ├── gateway/             # OpenAI-compatible HTTP server (binds 127.0.0.1:8787)
│   ├── routing/             # upstream selection + strategy (round_robin / failover)
│   ├── retry/               # 429 detection, cooldown, retry-on-next-proxy logic
│   ├── proxymanager/        # proxy pool state, rotation, cooldown tracking
│   ├── proxyimport/         # parse & load proxy files / dashboard imports
│   ├── geo/                 # per-proxy exit-IP geo / country lookup
│   ├── runtime/             # process runtime wiring and lifecycle
│   ├── tuiapp/              # Bubble Tea dashboard application
│   │   ├── pages/           # dashboard pages (overview, proxies, routes, requests, models…)
│   │   └── ui/              # shared TUI styles and widgets
│   └── …                    # config, health, metrics, security, transport, stream helpers
├── data/
│   └── proxies/             # drop your *.txt proxy files here (gitignored)
│       └── EXAMPLE.txt.example
├── profiles/                # saved runtime profiles (gitignored except index.example.json)
├── nlwproxy.json            # active config
└── nlwproxy.example.json    # template config to copy from
```

Key `nlwproxy.json` fields at a glance:

- `server.local_token_env` — name of the env var holding the token clients must present.
- `upstreams[].api_key_env` — name of the env var holding your provider API key.
- `proxy_only` — when `true`, all traffic must go through a proxy.
- `routing.strategy` — `round_robin` or `failover`.

## Configuration

NLW Proxy is configured via `nlwproxy.json`. Copy the template to get started:

```bash
cp nlwproxy.example.json nlwproxy.json
```

Example config:

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

| Field | Meaning |
| --- | --- |
| `server.listen` | Address the gateway binds to. Keep it `127.0.0.1` (localhost only). |
| `server.local_token_env` | **Name** of the env var whose value clients send as their API key. |
| `routing.strategy` | `round_robin` (default) or `failover`. |
| `proxy_only` | `true` = fail-closed; never connect without a proxy. |
| `upstreams[].base_url` | Your provider's OpenAI-compatible base URL. |
| `upstreams[].api_key_env` | **Name** of the env var holding the provider API key. |
| `upstreams[].priority` / `weight` | Ordering / weighting hints for routing. |
| `upstreams[].enabled` | Toggle this upstream on/off. |

> **Important:** `api_key_env` and `local_token_env` hold **environment-variable names, not secrets**. NLW Proxy reads the actual key/token from the environment at runtime. This keeps credentials out of the config file and out of version control.

### Setting the environment variables

Set the provider key and the local gateway token. On **Windows** (persist with `setx`, then open a new terminal):

```bat
setx MYPROVIDER_API_KEY "sk-your-real-provider-key"
setx NLW_PROXY_LOCAL_TOKEN "choose-a-strong-local-token"
```

On **macOS / Linux** (add to your shell profile):

```bash
export MYPROVIDER_API_KEY="sk-your-real-provider-key"
export NLW_PROXY_LOCAL_TOKEN="choose-a-strong-local-token"
```

- `MYPROVIDER_API_KEY` — the real key for your upstream provider (name must match `api_key_env`).
- `NLW_PROXY_LOCAL_TOKEN` — the token your clients (OpenCode / Claude Code) send to the gateway (name must match `local_token_env`).

## Adding Proxies

You have two ways to load proxies:

**1. Drop files into `data/proxies/`.** Create any number of `*.txt` files there. Every file is auto-loaded on startup, deduplicated by `host:port`, and merged. See [`data/proxies/EXAMPLE.txt.example`](data/proxies/EXAMPLE.txt.example) for the format:

```
# host:port
# host:port:username:password        (authenticated — recommended)
# http://host:port
# http://username:password@host:port
# socks5://host:port
203.0.113.10:8080:user1:pass1
203.0.113.11:8080:user1:pass1
socks5://198.51.100.5:1080
```

Lines starting with `#` and blank lines are ignored.

**2. Import from the dashboard.** Open the **Proxies** page and press **`i`** to import.

Once loaded:

- Press **`t`** on the Proxies page to **health-test** every proxy.
- **Only alive proxies enter the round-robin.** Dead proxies are excluded.
- A proxy that returns **429** is put on **cooldown automatically** and skipped until it recovers.

## Running

Launch the dashboard (default, no arguments):

```bash
nlwproxy
```

### Dashboard pages

| Page | What it shows |
| --- | --- |
| **Overview** | Live status + **copy-ready Base URL** and **API-key reference** for your client. |
| **Proxies** | The proxy pool — status, geo/country, cooldowns; test and import from here. |
| **Routes** | Upstreams and the active routing strategy. |
| **Requests** | Live request stream flowing through the gateway. |
| **Models** | Models available from your upstream(s). |
| **Logs** | Runtime logs. |
| **Settings** | Configuration and runtime toggles. |

### Key bindings

| Key | Action |
| --- | --- |
| `Tab` | Move focus between panes |
| `↑ ↓ ← →` | Navigate within a pane |
| `Enter` | Open / select |
| `/` | Search |
| `t` | Health-test proxies |
| `i` | Import proxies |
| `f` | Freeze / pause live updates |
| `q` | Quit |
| `Ctrl+Shift+C` | Copy the selected connection detail |

## Connect OpenCode

The gateway is OpenAI-compatible and served at **`http://127.0.0.1:8787/v1`**. Add it as a provider in your OpenCode config (`opencode.json`). Use your `NLW_PROXY_LOCAL_TOKEN` **value** as the API key:

```json
{
  "provider": {
    "nlwproxy": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "NLW Proxy",
      "options": {
        "baseURL": "http://127.0.0.1:8787/v1",
        "apiKey": "your-NLW_PROXY_LOCAL_TOKEN-value"
      },
      "models": {
        "your-model-id": {
          "name": "Your Model"
        }
      }
    }
  }
}
```

Replace `your-model-id` with a model your upstream provider exposes (check the **Models** page in the dashboard).

## Connect Claude Code

Point Claude Code at the gateway via its `settings.json` `env` block. Use your `NLW_PROXY_LOCAL_TOKEN` **value** as the auth token:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:8787/v1",
    "ANTHROPIC_AUTH_TOKEN": "your-NLW_PROXY_LOCAL_TOKEN-value",
    "ANTHROPIC_MODEL": "your-model-id"
  }
}
```

## Generic OpenAI SDK Usage

Any OpenAI-compatible SDK works — just point `base_url` / `baseURL` at the gateway and use your local token as the key.

**Python:**

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8787/v1",
    api_key="your-NLW_PROXY_LOCAL_TOKEN-value",
)

resp = client.chat.completions.create(
    model="your-model-id",
    messages=[{"role": "user", "content": "Hello through the proxy!"}],
)
print(resp.choices[0].message.content)
```

**Node.js:**

```js
import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "http://127.0.0.1:8787/v1",
  apiKey: "your-NLW_PROXY_LOCAL_TOKEN-value",
});

const resp = await client.chat.completions.create({
  model: "your-model-id",
  messages: [{ role: "user", content: "Hello through the proxy!" }],
});
console.log(resp.choices[0].message.content);
```

## How It Works

Request lifecycle:

```
  ┌────────────┐       ┌───────────────────────┐       ┌──────────────────┐       ┌──────────────────┐
  │  Client    │  ───► │  NLW Gateway :8787     │  ───► │  Healthy proxy   │  ───► │  Upstream        │
  │ (OpenCode /│       │  (OpenAI-compatible)   │       │  (round-robin)   │       │  provider        │
  │  Claude    │       │                        │       │                  │       │                  │
  │  Code)     │  ◄─── │  validates local token │  ◄─── │  returns response│  ◄─── │  responds        │
  └────────────┘       └───────────────────────┘       └──────────────────┘       └──────────────────┘
                                  │
                                  │  on 429 from the chosen proxy:
                                  │  put that proxy on cooldown, pick the
                                  └► next healthy proxy, and retry.
```

1. Client sends an OpenAI-style request to the gateway with the local token.
2. Gateway validates the token and picks the next healthy proxy (round-robin).
3. Request is forwarded through that proxy to the upstream provider.
4. On `429`, the proxy is cooled down and the request is retried on the next healthy proxy.

## Security Notes

- **Proxy files and local env launchers are gitignored.** `data/proxies/*.txt`, `*.local.*`, and `.nlwproxy-env.cmd` are excluded by `.gitignore` because they contain credentials.
- **Never commit real keys.** `api_key_env` / `local_token_env` store env-var *names*; the actual secrets live only in your environment.
- **Localhost only.** The gateway binds to `127.0.0.1` — it is not reachable from other machines on your network.

## Troubleshooting

- **Proxies stuck "testing"** — a slow or unreachable proxy is still being probed. Give it a moment; ones that don't respond are marked dead and excluded from rotation. Re-run the test with **`t`** on the Proxies page.
- **All proxies 429 / `NO_HEALTHY_PROXY`** — every proxy is on cooldown. The error reports the **soonest recovery** time; wait for a proxy to come off cooldown, add more proxies to `data/proxies/`, or slow your request rate.
- **`401` / `403` from upstream** — a bad or missing key. Verify `MYPROVIDER_API_KEY` (provider key) and `NLW_PROXY_LOCAL_TOKEN` (client token) are set in the environment and that their names match `api_key_env` / `local_token_env` in `nlwproxy.json`. On Windows, remember `setx` only affects **new** terminals.

---

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea). Run `nlwproxy help` for the full CLI reference.

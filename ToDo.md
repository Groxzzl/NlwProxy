# NLW Proxy — Product Requirements Document & Execution Plan

> **Document ID:** NLP-PRD-001  
> **Status:** Approved / In Development  
> **Version:** 1.0  
> **Platform MVP:** Windows 10/11 x64  
> **Project path:** `D:\TEMPLATE WEBSITE\money\NlwProxy`  
> **Primary executable:** `nlwproxy.exe`

---

## 1. Executive Summary

**NLW Proxy** is a local, OpenCode-only network gateway and proxy manager. It sits between OpenCode and an OpenAI-compatible model provider, then supplies controlled routing through direct, HTTP(S), or SOCKS5 connections owned or explicitly configured by the user.

The application focuses on connection reliability, health checking, latency-aware routing, safe failover, streaming integrity, diagnostics, and local observability. It is distributed as one portable Windows executable and requires no paid infrastructure.

```text
OpenCode
   │ OpenAI-compatible HTTP + SSE
   ▼
NLW Proxy — 127.0.0.1:8787
   ├── local authentication
   ├── OpenCode-only validation
   ├── request validation
   ├── health scoring
   ├── route selection
   ├── circuit breaker
   ├── safe network failover
   ├── SSE streaming relay
   └── local metrics
   │
   ├── Direct connection
   ├── Authorized HTTP/HTTPS proxy
   └── Authorized SOCKS5 proxy
   │
   ▼
OpenCode model provider
```

### Product promise

- OpenCode setup in under three minutes.
- One portable binary with no runtime dependency.
- Reliable streaming without buffering full responses.
- Clear connection state and diagnostic information.
- No prompt or response content stored locally.
- No system-wide proxy changes.
- No replay of authentication, quota, or rate-limit errors through another route.

---

## 2. Problem Statement

OpenCode users who work through multiple network paths face recurring operational problems:

1. A proxy can stop responding without a clear status indicator.
2. Latency changes over time and manual route selection becomes inefficient.
3. Configuration is scattered across environment variables and OpenCode files.
4. Streaming failures are difficult to attribute to OpenCode, the proxy, or the upstream provider.
5. Connection switching usually requires configuration edits or process restarts.
6. Proxy credentials can accidentally leak through logs or configuration output.
7. Generic proxy tools are system-wide and are not designed around OpenCode request semantics.
8. Blind retries can duplicate side effects or mishandle streamed responses.
9. Public proxy lists are unreliable and expose traffic to untrusted operators.
10. Existing tools rarely provide a local dashboard tailored to OpenCode.

---

## 3. Goals

### 3.1 Product goals

- Provide a local OpenAI-compatible endpoint dedicated to OpenCode.
- Detect, back up, patch, validate, and restore OpenCode configuration.
- Support direct, HTTP CONNECT, HTTPS proxy, and SOCKS5 routes.
- Continuously measure route health and latency.
- Select a healthy route using explicit routing policies.
- Fail over only for safe network and transient upstream failures.
- Relay SSE responses immediately and preserve cancellation semantics.
- Display real-time route, request, latency, and failure information.
- Store metadata locally in a bounded SQLite database.
- Ship as a portable Windows executable.
- Keep development and operation costs at Rp0.

### 3.2 Engineering goals

- Gateway overhead below 3 ms at p50 and 10 ms at p95.
- Idle memory below 40 MB.
- Startup below 500 ms.
- Support at least 100 concurrent requests.
- Graceful shutdown within 15 seconds.
- No data races under Go race testing.
- No secrets or conversation content in logs, metrics, diagnostics, or panic output.

---

## 4. Non-Goals

NLW Proxy will not:

- Scrape or harvest public proxy lists.
- Rotate IP addresses to obtain additional free quota.
- Retry `429`, quota-exhausted, `401`, `402`, or `403` responses through another IP.
- Create accounts or API keys in bulk.
- Spoof device, account, browser, or service identity.
- Modify prompt or response content.
- Store conversation content.
- Install a system-wide proxy.
- Accept arbitrary LAN clients by default.
- Support applications other than OpenCode in MVP strict mode.
- Disable TLS verification.
- Auto-run from startup without explicit user configuration.

---

## 5. Target Users

### Solo developer

Needs a stable OpenCode connection with understandable errors and automatic recovery from ordinary network failures.

### Power user

Owns several legitimate routes such as direct internet, a local SOCKS5 service, office HTTP proxy, or SSH tunnel. Needs health scoring and policy-based selection.

### Technician

Configures OpenCode for multiple local workstations. Needs a portable binary, predictable setup, rollback, and redacted diagnostics.

---

## 6. Supported Free Route Sources

| Route | Cost | Stability | Notes |
|---|---:|---:|---|
| Direct internet | Rp0 | High | Default route when available |
| Tor local SOCKS5 | Rp0 | Medium/low | Requires local Tor; slower and often blocked |
| Cloudflare WARP local proxy mode | Rp0 | High | Only when exposed locally by the user's own setup |
| SSH dynamic forwarding | Rp0* | High | `ssh -D`; requires an authorized SSH host |
| Private LAN SOCKS/HTTP proxy | Rp0 | Varies | User-owned or organization-authorized |
| Ollama/vLLM local provider | Rp0 | High | Local inference; no external proxy required |

`*` The software is free; infrastructure availability belongs to the user.

Public proxy aggregation is excluded because availability and integrity cannot be trusted.

---

## 7. Product Principles

1. **OpenCode-first** — every command and screen is optimized for OpenCode.
2. **Local-first** — configuration, state, and metrics remain on the workstation.
3. **Zero-content logging** — metadata only.
4. **Explicit routing** — every selected route has a visible reason.
5. **Fail closed** — no valid route means the request stops clearly.
6. **Streaming-native** — chunks are relayed as received.
7. **Keyboard-first** — full terminal operation.
8. **Portable by default** — one binary and one project data directory.
9. **Safe retries** — retry classification is conservative.
10. **Professional operations UX** — infrastructure-console visual language, not fake hacker styling.

---

## 8. System Architecture

```text
┌────────────────────────────────────────────────────────────────────┐
│                         USER WORKSTATION                           │
│                                                                    │
│  ┌──────────────────┐                                              │
│  │     OpenCode     │                                              │
│  └────────┬─────────┘                                              │
│           │ HTTP + SSE                                             │
│           ▼                                                        │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │ NLW PROXY LOCAL GATEWAY — 127.0.0.1:8787                   │  │
│  │                                                              │  │
│  │ Local Auth → Validator → Compatibility Layer → SSE Relay    │  │
│  │                         │                                    │  │
│  │                         ▼                                    │  │
│  │ Route Policy ↔ Health Engine ↔ Circuit Breaker ↔ Retry      │  │
│  │                         │                                    │  │
│  │             ┌───────────┼────────────┐                       │  │
│  │             ▼           ▼            ▼                       │  │
│  │          Direct       HTTP(S)      SOCKS5                    │  │
│  └─────────────┬───────────┬────────────┬───────────────────────┘  │
│                └───────────┼────────────┘                          │
│                            ▼                                       │
│                OpenCode model provider                             │
│                                                                    │
│  SQLite metrics       YAML/JSON config       Secret references     │
└────────────────────────────────────────────────────────────────────┘
```

### Trust boundaries

1. OpenCode client boundary.
2. Loopback gateway boundary.
3. Local secret/configuration boundary.
4. Network transport boundary.
5. External upstream provider boundary.

---

## 9. Core Modules

### 9.1 CLI shell

Commands:

```text
nlwproxy
├── init
├── setup
├── serve
├── status
├── dashboard
├── doctor
├── config
│   ├── check
│   ├── path
│   └── print-redacted
├── proxy
│   ├── add
│   ├── edit
│   ├── remove
│   ├── list
│   ├── enable
│   ├── disable
│   ├── select
│   └── test
├── route
│   ├── status
│   ├── set-strategy
│   └── set-priority
├── logs
├── stats
└── uninstall
```

Current foundation already contains:

- `init`
- `config check`
- `status`
- strict config validation
- loopback validation
- OpenCode-only enforcement settings

### 9.2 OpenCode configuration adapter

Responsibilities:

- Discover supported OpenCode configuration locations.
- Parse existing JSON without deleting unrelated providers.
- Generate a diff preview.
- Create a timestamped backup before writes.
- Patch the NLW Proxy provider atomically.
- Validate resulting JSON.
- Restore the exact original file on rollback.
- Store backup checksum.

### 9.3 Local authentication

- Gateway binds to loopback only.
- Local token contains at least 256 bits of randomness.
- Token comparison is constant-time.
- Browser CORS is disabled.
- Strict mode checks an OpenCode client marker.
- Invalid tokens return `401` without route processing.

### 9.4 API compatibility layer

Required endpoints:

```text
GET  /health
GET  /v1/models
POST /v1/chat/completions
POST /v1/responses
```

Requirements:

- Preserve supported OpenAI-compatible fields.
- Support streaming and non-streaming responses.
- Remove hop-by-hop headers.
- Enforce maximum body size.
- Do not log body content.
- Preserve safe provider error bodies while scrubbing secrets.

### 9.5 Transport layer

Transport implementations:

- Direct.
- HTTP CONNECT.
- HTTPS proxy.
- SOCKS5.

Each route has:

- Stable ID and human-readable name.
- Type and secret reference.
- Priority and optional weight.
- Enabled state.
- Maximum concurrency.
- Health state.
- Last successful use.
- Last redacted error.

### 9.6 Health engine

Probe stages:

```text
Parse configuration
      ▼
Resolve DNS
      ▼
Establish TCP
      ▼
Create proxy tunnel
      ▼
Perform TLS handshake
      ▼
Optional lightweight upstream probe
      ▼
Calculate score and state
```

Health formula:

```text
score = availability × 0.45
      + latency      × 0.25
      + error rate   × 0.20
      + stability    × 0.10
```

### 9.7 Routing engine

Strategies:

- `manual`
- `priority`
- `lowest_latency`
- `round_robin`
- `least_active`
- `sticky_session`

Latency uses EWMA:

```text
EWMA(new) = α × latest + (1 - α) × previous
Default α = 0.25
```

Sticky sessions use a locally salted hash, never raw conversation content.

### 9.8 Circuit breaker

States:

```text
UNKNOWN → HEALTHY → DEGRADED → OPEN → HALF_OPEN → HEALTHY
                          └─────────────── failure ─────▶ OPEN
```

Additional terminal states:

- `DISABLED`
- `AUTH_REQUIRED`

Default policy:

- Open after four consecutive qualifying failures.
- Cooldown for 60 seconds.
- Require two successful recovery probes.

### 9.9 Retry policy

#### Retryable

- DNS timeout.
- TCP refusal/reset before request acceptance.
- Proxy tunnel failure.
- TLS handshake failure.
- `502`, `503`, or `504`.
- Stream failure before the first response byte.

#### Non-retryable across another route

- `400`, `401`, `402`, `403`, `409` with uncertain side effects.
- `429` and quota exhausted.
- A stream that already emitted data.
- Tool calls whose execution status is unknown.

Default maximum:

```text
Network attempts: 2
502/503/504 retry: 1
Initial backoff: 250 ms
Maximum backoff: 2 s
Jitter: enabled
429 retry: disabled
```

### 9.10 SSE relay

Requirements:

- Flush each chunk immediately.
- Preserve event ordering.
- Handle partial frames.
- Cancel upstream when OpenCode disconnects.
- Never replay after response bytes have been emitted.
- Record TTFT without recording content.
- Apply bounded read/write timeouts compatible with long model streams.

### 9.11 Metrics and storage

Stored request metadata:

- Request ID.
- Salted session hash.
- Route ID.
- Endpoint.
- HTTP status.
- Start time.
- TTFT.
- Total duration.
- Request/response byte counts.
- Retry count.
- Redacted error code.

Never stored:

- Prompt.
- Response.
- API key.
- Authorization header.
- Cookie.
- Proxy password.
- Raw OpenCode session identifier.

### 9.12 Terminal dashboard

Primary tabs:

1. Overview.
2. Connections.
3. Requests.
4. Health.
5. Logs.
6. Settings.
7. Diagnostics.

Required status symbols:

```text
● HEALTHY
◐ DEGRADED
○ OPEN
× AUTH_REQUIRED
— DISABLED
```

Colors complement symbols; color is not the sole status indicator.

---

## 10. User Experience

### First run

```text
nlwproxy init
    │
    ├── create project data directories
    ├── generate default config
    ├── generate local token reference
    ├── validate loopback binding
    └── print next steps
```

### OpenCode setup

```text
nlwproxy setup --dry-run
    │
    ├── discover OpenCode config
    ├── validate current config
    ├── show proposed diff
    └── write nothing

nlwproxy setup
    │
    ├── create backup + checksum
    ├── patch NLW Proxy provider
    ├── validate result
    ├── start temporary health check
    └── report readiness
```

### Add a route

```bash
nlwproxy proxy add tor \
  --type socks5 \
  --url-env NLW_PROXY_TOR \
  --priority 40
```

### Run

```bash
nlwproxy serve
nlwproxy dashboard
```

### Rollback

```bash
nlwproxy setup --rollback
```

---

## 11. Configuration Specification

Recommended configuration:

```json
{
  "version": 1,
  "server": {
    "listen": "127.0.0.1:8787",
    "localTokenEnv": "NLW_PROXY_LOCAL_TOKEN",
    "strictOpenCodeClient": true,
    "requestTimeoutSeconds": 300,
    "shutdownTimeoutSeconds": 15,
    "maxBodyBytes": 20971520
  },
  "opencode": {
    "configPath": "auto",
    "backupBeforePatch": true,
    "providerName": "nlwproxy",
    "modelAlias": "opencode-route"
  },
  "upstream": {
    "baseUrlEnv": "NLW_UPSTREAM_BASE_URL",
    "apiKeyEnv": "NLW_UPSTREAM_API_KEY"
  },
  "connections": [
    {
      "name": "direct",
      "type": "direct",
      "priority": 100,
      "enabled": true,
      "maxConcurrency": 8
    }
  ],
  "routing": {
    "strategy": "priority",
    "stickySessions": true,
    "stickyTtlSeconds": 1800
  },
  "health": {
    "intervalSeconds": 30,
    "timeoutSeconds": 5,
    "failureThreshold": 4,
    "recoverySuccesses": 2,
    "circuitCooldownSeconds": 60
  },
  "retry": {
    "networkAttempts": 2,
    "retryStatuses": [502, 503, 504],
    "retry429": false,
    "honorRetryAfter": true
  },
  "logging": {
    "level": "info",
    "format": "pretty",
    "logRequestBody": false,
    "logResponseBody": false,
    "redactSecrets": true,
    "retentionDays": 7
  },
  "storage": {
    "path": "./data/nlwproxy.db",
    "maxSizeBytes": 104857600,
    "retentionDays": 7
  }
}
```

---

## 12. OpenCode Integration

Example provider entry:

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
          "name": "NLW Managed Route"
        }
      }
    }
  },
  "model": "nlwproxy/opencode-route"
}
```

Actual patching must adapt to the currently installed OpenCode configuration schema and preserve unrelated settings.

---

## 13. Security Requirements

- Bind only to `127.0.0.1` or `::1` by default.
- Reject `0.0.0.0` unless future explicit advanced mode is implemented.
- Use constant-time local-token comparison.
- Disable CORS.
- Reject browser origins in strict mode.
- Enforce body-size limits.
- Strip hop-by-hop headers.
- Reject CRLF in proxy URLs and headers.
- Validate upstream scheme and host.
- Restrict diagnostics paths to the project data directory.
- Store credentials via environment variables or Windows Credential Manager references.
- Reject plaintext proxy credentials by default.
- Redact bearer tokens and URL credentials.
- Use atomic writes for configuration.
- Generate SHA-256 checksums for releases and backups.
- Avoid panic dumps containing request data.
- Run static analysis and dependency checks in CI.

---

## 14. Error Taxonomy

| Code | Category | Description |
|---|---|---|
| `NLP-CFG-001` | Config | OpenCode config not found |
| `NLP-CFG-002` | Config | Invalid OpenCode JSON |
| `NLP-CFG-003` | Config | Invalid NLW Proxy config |
| `NLP-NET-001` | DNS | Proxy host resolution failed |
| `NLP-NET-002` | TCP | Connection refused/reset |
| `NLP-NET-003` | TLS | TLS handshake failed |
| `NLP-PRX-001` | Proxy | Proxy authentication failed |
| `NLP-PRX-002` | Proxy | CONNECT denied |
| `NLP-UP-001` | Upstream | Provider unavailable |
| `NLP-UP-002` | Upstream | Rate limited; cooldown applied |
| `NLP-UP-003` | Upstream | Authentication rejected |
| `NLP-STR-001` | Stream | Failure before first byte |
| `NLP-STR-002` | Stream | Client disconnected |
| `NLP-SEC-001` | Security | Non-OpenCode client rejected |
| `NLP-SEC-002` | Security | Invalid local token |
| `NLP-DB-001` | Storage | Metrics database unavailable |

---

## 15. Failure Handling

| Scenario | Required behavior |
|---|---|
| Route fails before request | Select next eligible healthy route |
| Route fails after streaming begins | Stop and report; do not replay |
| DNS fails | Record health failure and update circuit |
| Proxy credentials fail | Set `AUTH_REQUIRED` |
| Provider returns `429` | Honor `Retry-After`; do not switch route to replay |
| Port is occupied | Report owner/port and offer alternate config preview |
| OpenCode config is invalid | Abort before writing |
| SQLite is locked | Buffer bounded metadata; keep request path operational |
| Disk is full | Disable persistence and surface a dashboard alert |
| System wakes from sleep | Trigger immediate health probes |
| Ctrl+C | Graceful shutdown and cancellation |
| Proxy intercepts TLS | Fail certificate validation |

---

## 16. Performance Targets

| Metric | Target |
|---|---:|
| Gateway overhead p50 | `< 3 ms` |
| Gateway overhead p95 | `< 10 ms` |
| Idle memory | `< 40 MB` |
| Startup | `< 500 ms` |
| Concurrent requests | `100` |
| CPU idle | `< 1%` |
| Shutdown deadline | `15 s` |
| Binary size | `< 30 MB` preferred |
| Health-check concurrency | `5` maximum |

---

## 17. Repository Structure

```text
NlwProxy/
├── cmd/
│   └── nlwproxy/
│       └── main.go
├── internal/
│   ├── app/
│   ├── cli/
│   ├── config/
│   ├── opencode/
│   ├── gateway/
│   ├── transport/
│   ├── routing/
│   ├── health/
│   ├── retry/
│   ├── stream/
│   ├── metrics/
│   ├── storage/
│   ├── security/
│   ├── diagnostics/
│   └── tui/
├── migrations/
├── config/
├── docs/
│   └── Nlw_Proxy.excalidraw
├── test/
│   ├── fixtures/
│   ├── integration/
│   ├── fault/
│   └── security/
├── dist/
├── .github/workflows/
├── go.mod
├── go.sum
├── README.md
├── ToDo.md
└── LICENSE
```

---

## 18. Test Strategy

### Unit tests

- Strict config parsing.
- Config validation.
- Secret redaction.
- Route scoring.
- EWMA latency.
- Priority selection.
- Round-robin behavior.
- Sticky-session hashing.
- Circuit-breaker transitions.
- Retry classification.
- Header filtering.
- OpenCode config patch/rollback.

### Integration tests

```text
OpenCode fixture
      │
      ▼
NLW Proxy gateway
      │
      ├── healthy mock upstream
      ├── timeout proxy
      ├── authentication failure
      ├── SSE reset before first byte
      ├── SSE reset after first byte
      ├── 429 provider
      └── 502/503/504 provider
```

### Fault-injection tests

- Delayed DNS.
- TCP reset.
- Partial SSE frame.
- Slow upstream.
- Invalid chunk encoding.
- Expired proxy authentication.
- Disk full.
- SQLite lock.
- Abrupt process exit.
- Duplicate request ID.
- Upstream close after headers.

### Security tests

- Loopback-only bind.
- CORS rejection.
- Invalid token.
- Header injection.
- CRLF in proxy URL.
- SSRF through upstream configuration.
- Path traversal in diagnostic export.
- Secret-leak snapshot tests.
- Oversized body rejection.
- Malformed SSE handling.
- Diagnostic archive path safety.

### Verification commands

```bash
go test ./...
go test -race ./...
go vet ./...
go build -trimpath -ldflags="-s -w" -o dist/nlwproxy.exe ./cmd/nlwproxy
./dist/nlwproxy.exe config check
./dist/nlwproxy.exe status
```

---

## 19. Acceptance Criteria

### CLI and setup

- [ ] `nlwproxy init` creates a valid default configuration.
- [ ] `nlwproxy config check` detects all invalid fields clearly.
- [ ] `nlwproxy setup --dry-run` makes no filesystem changes.
- [ ] OpenCode config is backed up before patching.
- [ ] Rollback restores the original file byte-for-byte.
- [ ] Setup completes in under three minutes.

### Gateway

- [ ] `/health` returns process and route status.
- [ ] `/v1/models` returns the configured OpenCode route alias.
- [ ] Chat completion requests pass through successfully.
- [ ] Responses API requests pass through successfully.
- [ ] SSE chunks flush immediately.
- [ ] Client disconnect cancels upstream.
- [ ] Request/response content is never written to disk.
- [ ] Invalid local token returns `401`.
- [ ] Non-OpenCode clients are rejected in strict mode.

### Routing

- [ ] Disabled, open, and authentication-failed routes are never selected.
- [ ] Priority routing is deterministic.
- [ ] Lowest-latency routing uses EWMA.
- [ ] Round-robin uses only healthy eligible routes.
- [ ] Sticky sessions remain stable for their TTL.
- [ ] Network retries remain within configured bounds.
- [ ] `429`, quota, and authentication errors never trigger route replay.

### TUI

- [ ] Overview displays service status and active route.
- [ ] Connection screen displays score, state, latency, and active requests.
- [ ] Request screen displays metadata only.
- [ ] Diagnostic screen produces redacted output.
- [ ] Every action is keyboard accessible.
- [ ] Terminal resizing does not crash.
- [ ] Status remains understandable without color.

### Security

- [ ] No secrets appear in logs, database, diagnostics, or panic output.
- [ ] Plaintext credentials are rejected by default.
- [ ] CORS is disabled.
- [ ] Gateway binds to loopback.
- [ ] Release includes SHA-256 checksum.
- [ ] Static analysis and dependency checks pass.

### Packaging

- [ ] `nlwproxy.exe` runs without Go installed.
- [ ] Portable mode keeps state under the selected local data folder.
- [ ] Windows Terminal, CMD, and Git Bash smoke tests pass.
- [ ] Build is reproducible through documented commands.

---

## 20. Execution Plan

### Phase 1 — Foundation

- [x] Create project directory.
- [x] Initialize Go module.
- [x] Implement CLI entry point.
- [x] Implement `init`.
- [x] Implement `config check`.
- [x] Implement basic `status`.
- [x] Add strict configuration validation.
- [x] Build initial `nlwproxy.exe`.
- [x] Add foundation tests.

### Phase 2 — Gateway core

- [ ] Implement local HTTP server.
- [ ] Implement local token authentication.
- [ ] Implement strict OpenCode client validation.
- [ ] Implement `/health`.
- [ ] Implement `/v1/models`.
- [ ] Implement `/v1/chat/completions`.
- [ ] Implement `/v1/responses`.
- [ ] Implement header filtering.
- [ ] Implement body limits.
- [ ] Implement graceful shutdown.

### Phase 3 — Transports

- [ ] Implement direct transport.
- [ ] Implement HTTP CONNECT transport.
- [ ] Implement HTTPS proxy transport.
- [ ] Implement SOCKS5 transport.
- [ ] Implement secret references.
- [ ] Implement connection concurrency limits.
- [ ] Add transport unit tests.

### Phase 4 — Health and policy

- [ ] Implement health probes.
- [ ] Implement health score calculation.
- [ ] Implement connection state machine.
- [ ] Implement circuit breaker.
- [ ] Implement priority routing.
- [ ] Implement lowest-latency routing.
- [ ] Implement round-robin routing.
- [ ] Implement least-active routing.
- [ ] Implement sticky sessions.
- [ ] Implement safe retry classifier.
- [ ] Prove `429` and auth errors never cause route replay.

### Phase 5 — Streaming

- [ ] Implement streaming request forwarding.
- [ ] Implement SSE chunk relay.
- [ ] Implement immediate flushing.
- [ ] Implement client cancellation propagation.
- [ ] Track TTFT.
- [ ] Handle partial and malformed frames.
- [ ] Add pre-stream and mid-stream failure tests.

### Phase 6 — OpenCode setup

- [ ] Detect live OpenCode configuration path.
- [ ] Validate active configuration schema.
- [ ] Implement backup and checksum.
- [ ] Implement dry-run diff.
- [ ] Implement atomic patch.
- [ ] Implement rollback.
- [ ] Implement uninstall.
- [ ] Add compatibility fixtures.

### Phase 7 — Metrics and diagnostics

- [ ] Add SQLite dependency and migrations.
- [ ] Store bounded request metadata.
- [ ] Store health samples.
- [ ] Implement retention cleanup.
- [ ] Implement log redaction.
- [ ] Implement diagnostics export.
- [ ] Add secret-leak tests.

### Phase 8 — Professional TUI

- [ ] Build dashboard shell.
- [ ] Build overview screen.
- [ ] Build connection manager.
- [ ] Build request metrics view.
- [ ] Build health view.
- [ ] Build logs view.
- [ ] Build settings view.
- [ ] Build diagnostic view.
- [ ] Test resize and keyboard accessibility.

### Phase 9 — Documentation and design

- [ ] Finalize README.
- [ ] Add installation guide.
- [ ] Add OpenCode setup guide.
- [ ] Add route configuration examples.
- [ ] Add Tor, WARP-local, and SSH SOCKS examples.
- [ ] Copy `Nlw_Proxy.excalidraw` into `docs/`.
- [ ] Export architecture SVG and PNG.
- [ ] Add troubleshooting matrix.

### Phase 10 — Quality and release

- [ ] Run all unit tests.
- [ ] Run integration tests.
- [ ] Run race detector.
- [ ] Run fault-injection tests.
- [ ] Run security tests.
- [ ] Run `go vet`.
- [ ] Build optimized portable EXE.
- [ ] Generate SHA-256 checksum.
- [ ] Smoke-test on Windows Terminal.
- [ ] Smoke-test on CMD.
- [ ] Smoke-test on Git Bash.
- [ ] Create release notes.

---

## 21. Dependency Graph

```text
st-001 Foundation/config/CLI
   │
   ├────────▶ st-002 Gateway API
   ├────────▶ st-003 OpenCode adapter
   └────────▶ st-004 Security model
                  │
                  ▼
st-005 Transport layer ◀──── st-002 + st-004
   │
   ├────────▶ st-006 Health engine
   └────────▶ st-007 SSE relay
                  │
                  ▼
st-008 Routing/retry/circuit ◀──── st-005 + st-006
                  │
                  ├────────▶ st-009 Metrics/storage
                  ├────────▶ st-010 TUI
                  └────────▶ st-011 Setup/rollback
                                  │
                                  ▼
st-012 Integration/fault/security tests ◀──── all modules
                                  │
                                  ▼
st-013 Portable release + checksum + smoke test
```

---

## 22. Current Implementation Status

Verified foundation artifacts:

```text
go.mod
README.md
cmd/nlwproxy/main.go
internal/cli/cli.go
internal/cli/cli_test.go
internal/config/config.go
internal/config/config_test.go
nlwproxy.exe
ToDo.md
```

Reported successful foundation checks:

```text
go test ./...
go vet ./...
go build -o nlwproxy.exe ./cmd/nlwproxy
```

Two delegated implementation streams for gateway and TUI/docs encountered external HTTP `502` failures before returning reliable completion details. Their work must be inspected directly and treated as unverified until local tests pass.

---

## 23. Definition of Done

NLW Proxy is complete only when:

1. OpenCode can execute normal chat and tool-call workflows through the gateway.
2. Direct, HTTP(S), and SOCKS5 routes are covered by integration tests.
3. Streaming is immediate and cancellation-safe.
4. Network failures trigger only bounded, safe failover.
5. `429`, quota, and authentication errors never trigger route replay.
6. Setup, dry-run, backup, patch, rollback, and uninstall are verified.
7. No conversation content or secrets appear in persisted output.
8. The professional TUI works across supported Windows terminals.
9. All unit, integration, race, fault, and security tests pass.
10. A portable optimized `nlwproxy.exe` and SHA-256 checksum are produced.
11. Architecture Excalidraw and complete documentation are included.
12. A clean-machine smoke test succeeds without Go installed.

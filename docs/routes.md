# Authorized route setup

Use only routes you operate or are authorized to use. NlwProxy does not discover public proxies and rejects plaintext credentials embedded in route URLs.

Replace the sample provider URL and key variable with values for your authorized account.

## Direct

```sh
nlwproxy proxy add direct \
  --base-url https://api.openai.com/v1 \
  --api-key-env OPENAI_API_KEY \
  --priority 10
nlwproxy proxy test direct
```

No `proxy_url` is set. This is the safest and fastest default.

## Tor local SOCKS5

Install Tor from an official source and start its local SOCKS listener. The common endpoint is `127.0.0.1:9050`, but verify your installation.

```sh
curl --proxy socks5h://127.0.0.1:9050 https://check.torproject.org/api/ip
nlwproxy proxy add tor-local \
  --base-url https://api.openai.com/v1 \
  --proxy-url socks5h://127.0.0.1:9050 \
  --api-key-env OPENAI_API_KEY \
  --priority 40 \
  --enabled=false
nlwproxy proxy test tor-local
```

Use `socks5h` so DNS resolution occurs through the SOCKS endpoint. Tor is not a quota or authorization bypass; providers may block it.

## SSH dynamic forwarding

Use a server you own or are explicitly authorized to access:

```sh
ssh -N -D 127.0.0.1:1080 user@your-host.example
curl --proxy socks5h://127.0.0.1:1080 https://api.ipify.org
nlwproxy proxy add ssh-owned-host \
  --base-url https://api.openai.com/v1 \
  --proxy-url socks5h://127.0.0.1:1080 \
  --api-key-env OPENAI_API_KEY \
  --priority 30 \
  --enabled=false
```

Verify the SSH host fingerprint, prefer key authentication, and keep `-D` bound to loopback.

## User-authorized Cloudflare WARP local endpoint

NlwProxy does not install WARP or assume a port. Configure this only when your own WARP client/setup exposes a documented local HTTP or SOCKS endpoint.

```sh
# Replace 40000 with the endpoint shown by your WARP setup.
curl --proxy socks5h://127.0.0.1:40000 https://www.cloudflare.com/cdn-cgi/trace
nlwproxy proxy add warp-local \
  --base-url https://api.openai.com/v1 \
  --proxy-url socks5h://127.0.0.1:40000 \
  --api-key-env OPENAI_API_KEY \
  --priority 20 \
  --enabled=false
```

Do not guess a port or expose the endpoint beyond loopback.

## Private HTTP or SOCKS proxy

Use an organization-approved or contractually authorized endpoint:

```sh
nlwproxy proxy add office \
  --base-url https://api.openai.com/v1 \
  --proxy-url http://127.0.0.1:3128 \
  --api-key-env OPENAI_API_KEY \
  --priority 20
```

Current configuration rejects `user:password@host` URLs. Use a local authenticated proxy adapter or future credential-reference support rather than storing plaintext secrets.

## Route operations

```sh
nlwproxy proxy list
nlwproxy proxy enable tor-local
nlwproxy proxy disable direct
nlwproxy proxy edit tor-local --priority 50
nlwproxy route set-strategy failover
nlwproxy route set-priority direct 10
nlwproxy route status
```

## Operational rules

- Never retry `401`, `402`, `403`, or `429` through another route to evade account controls or quotas.
- Failover is for genuine transient transport/server failures such as DNS/TCP/TLS errors and `502`/`503`/`504` before streaming starts.
- Never discover, scrape, validate, or distribute public proxy lists.
- Log route names and metadata, never keys, authorization headers, prompts, or responses.

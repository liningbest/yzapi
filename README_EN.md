<p align="center">
  <img src="web/public/favicon.svg" width="72" alt="YZ AI Gateway">
</p>

<h1 align="center">YZ AI Gateway</h1>

<p align="center">
  A team-scale AI gateway. One base URL, one API key, every model and every coding tool.
</p>

<p align="center">
  <a href="#quick-start">Quick start</a> ·
  <a href="#coding-tools">Coding tools</a> ·
  <a href="docs/deploy.md">Deployment (zh)</a> ·
  <a href="docs/api.md">API reference (zh)</a> ·
  <a href="README.md">中文</a>
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white">
  <img alt="React" src="https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=black">
  <img alt="License" src="https://img.shields.io/badge/License-MIT-green">
  <img alt="Docker" src="https://img.shields.io/badge/Docker-linux%2Famd64%20%7C%20arm64-2496ED?logo=docker&logoColor=white">
</p>

---

YZ AI Gateway puts every LLM provider your team uses behind a single endpoint. Members call one address with their own key; admins manage accounts, permissions, quotas, usage and cost from one console. **A single Go binary with the frontend embedded, SQLite out of the box**, PostgreSQL for multi-instance deployments.

It is tuned for **coding tools**: Claude Code, Codex, OpenCode, Gemini CLI, Cline and Cursor point at the gateway without any changes. Protocols are converted inside the gateway, so an upstream that only speaks OpenAI can still serve Claude Code.

## Why

- **Configure once.** Members remember one URL and one key. Providers, key rotation and model retirements never touch the client.
- **See the cost.** Every call freezes its cost at the price in effect; reports break it down by user, group, model, provider and client. A built-in price table can be synced from LiteLLM and other sources.
- **Enforce limits.** Group concurrency, per-key concurrency, requests and tokens per minute, monthly token quota, key expiry and model allow-lists.
- **Fail over fast.** Ordered model groups, account health with cooldown and half-open probes, priority plus weight for canary traffic.
- **Never lose a count.** A local journal is written before the database, replayed on restart, deduplicated, and hourly rollups can be rebuilt and reconciled from the detail rows.
- **Stay compliant.** Multi-pattern keyword matching plus semantic sample matching, block or audit-only policy groups, optional Elasticsearch body archiving.

## Features

| Area | What you get |
|---|---|
| Unified API | `/v1/chat/completions`, `/v1/responses`, `/v1/messages`, `/v1/embeddings`, `/v1/images/generations`, `/v1/models`, native Gemini `/v1beta/models/*`; Bearer, `x-api-key` and `x-goog-api-key` auth |
| Protocol conversion | OpenAI Chat, OpenAI Responses, Anthropic Messages and Gemini converted pairwise, including streaming, tool calls, images, thinking content and budget mapping; same-protocol requests pass through untouched |
| Account pool | 26 built-in provider presets, several Chinese providers with dedicated Anthropic-compatible endpoints; model mapping, model discovery, priority, weight, concurrency cap, health, cooldown, half-open probe, cache-hit self check |
| Model groups | Ordered failover; the group name can be used as the model name |
| Users and groups | Seats, group concurrency, per-key concurrency, per-minute request and token limits, monthly token quota, authorised model groups; key expiry and model allow-list |
| Smart routing | Virtual model name (default `yz-auto`); context rules, local rules and vector similarity; sample management, decision preview, decision log |
| Compliance | Keyword matching (Aho-Corasick) plus semantic audit samples; policy groups with block / audit-only and risk levels; audit log; fails closed when rules cannot be loaded |
| Pricing | Built-in reference prices; preview-then-apply sync from LiteLLM, EasyCLIProxyAPI, any URL or an uploaded file; separate cache-read and cache-write prices; USD ledger with switchable display currency |
| Observability | Live overview, call logs (every account attempt, usage status, queue wait, upstream header time, first-content time, client tag), multi-dimensional usage reports, `/metrics` for Prometheus |
| Config versions | Automatic snapshot before every change to accounts, model groups, prices and settings; view, tag and roll back |
| User console | Model marketplace, setup guide with copy-ready configs per tool, API key management, personal usage and logs |
| Auditing | Optional Elasticsearch 8.x / 9.x request and response body archiving with size cap and retention |

## Quick start

### Docker

```bash
docker run -d --name yzapi --restart unless-stopped \
  -p 8080:8080 -v /opt/yzapi:/opt/yzapi \
  ghcr.io/liningbest/yzapi:latest
```

The first start prints a one-time admin password:

```bash
docker logs yzapi 2>&1 | grep -A3 "initial administrator"
```

Open `http://<host>:8080`, sign in as `admin`, and you will be asked to set a new password. `-e YZAPI_INITIAL_ADMIN_PASSWORD='YourStrongPass123'` sets it explicitly.

### Docker Compose

```bash
docker compose up -d
```

Add `--profile postgres` to start PostgreSQL alongside; see [docker-compose.yml](docker-compose.yml).

### Binary

Download the package for your platform from [Releases](../../releases), then:

```bash
YZAPI_DATA_DIR=/opt/yzapi ./yzapi
```

A systemd unit is in [deploy/systemd](deploy/systemd).

### From source

Requires Go 1.26+, Node 22+ and pnpm.

```bash
make all   # build the frontend and compile to bin/yzapi
make run   # run locally with ./data as the data directory
```

## Five steps to production

1. **Account pool**: add an upstream account. Pick the provider, paste the API key, map request model names to upstream model names, set priority and concurrency. Saving performs a minimal live check.
2. **Model groups**: order request models into groups. Groups are the unit of authorisation and failover.
3. **User groups**: set concurrency, token quota and authorised model groups.
4. **Users**: create members and assign them to groups.
5. **Settings → Basic**: set the public base URL members will use, e.g. `https://gateway.example.com/v1`.

Members sign in to the user console, create their own key, and copy the config for their tool from the setup guide.

## Coding tools

The setup guide in the user console renders these snippets with the admin-configured address.

**Claude Code** (`~/.claude/settings.json`)

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://gateway.example.com",
    "ANTHROPIC_AUTH_TOKEN": "sk-xxxx",
    "ANTHROPIC_MODEL": "claude-sonnet-4-5"
  }
}
```

**Codex** (`~/.codex/config.toml`)

```toml
model_provider = "yzapi"
model = "gpt-5-codex"

[model_providers.yzapi]
name = "YZ AI Gateway"
base_url = "https://gateway.example.com/v1"
env_key = "YZAPI_API_KEY"
wire_api = "responses"
```

**Gemini CLI**

```bash
export GEMINI_API_KEY="sk-xxxx"
export GOOGLE_GEMINI_BASE_URL="https://gateway.example.com"
```

**Any OpenAI-compatible client or SDK**

```bash
curl https://gateway.example.com/v1/chat/completions \
  -H "Authorization: Bearer sk-xxxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-chat","messages":[{"role":"user","content":"Hello"}]}'
```

Model names are resolved leniently (case, vendor prefix, date suffix). `count_tokens`, CORS and `Retry-After` pass-through are supported, and every response carries `X-Upstream-Account` / `X-Upstream-Model` / `X-Upstream-Protocol`. Cross-protocol limits are documented in [docs/api.md](docs/api.md).

## Providers

OpenAI, Anthropic, Google Gemini (native and OpenAI-compatible), DeepSeek, Alibaba Bailian, Tencent Cloud, Volcengine Ark, Zhipu, Moonshot, MiniMax, StepFun, Baidu Qianfan, SiliconFlow, xAI, Groq, Mistral, Together, Fireworks, Cerebras, OpenRouter, vLLM, Ollama, LM Studio, New API, plus custom OpenAI-compatible and Anthropic-compatible endpoints.

DeepSeek, Kimi, Zhipu, MiniMax and Bailian each have a dedicated Anthropic-compatible account type, so Claude Code can reach them on its own protocol.

## Configuration

Process-level settings come from environment variables; runtime parameters live in the admin Settings page.

| Variable | Default | Meaning |
|---|---|---|
| `YZAPI_LISTEN` | `0.0.0.0:8080` | Listen address |
| `YZAPI_DATA_DIR` | `/opt/yzapi` | Data directory (SQLite, credential key, JWT secret, metering journal) |
| `YZAPI_DB_DRIVER` | `sqlite` | `sqlite` or `postgres` |
| `YZAPI_DB_DSN` | | PostgreSQL DSN |
| `YZAPI_INITIAL_ADMIN_PASSWORD` | random | Admin password on first initialisation, never logged |
| `YZAPI_HTTP_PROXY` | | Force all upstream traffic through this proxy; otherwise standard `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` apply |
| `YZAPI_UPSTREAM_HTTP2` | `1` | Set to `0` to use HTTP/1.1 only toward upstreams |
| `YZAPI_JWT_SECRET` | generated | Must be shared across instances |
| `YZAPI_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `YZAPI_METRICS_TOKEN` | empty | When set, `/metrics` requires Bearer auth |
| `YZAPI_JOURNAL_FSYNC` | `interval` | Journal fsync about once per second; `always` fsyncs every record before returning |
| `YZAPI_SHUTDOWN_TIMEOUT` | `60` | Graceful shutdown wait in seconds |

Reset the admin password:

```bash
yzapi -reset-password admin
```

## Performance

Gateway overhead on the data plane, same-protocol Anthropic streaming against a zero-latency mock upstream, measured locally:

| Body | Direct TTFB | Via gateway | Overhead |
|---|---|---|---|
| 4 KB | 0.5 ms | 0.7 ms | 0.2 ms |
| 200 KB | 1.6 ms | 4.3 ms | 2.7 ms |
| 1 MB | 5.8 ms | 18.2 ms | 12.4 ms |

Real models take hundreds of milliseconds to seconds before the first token, so the gateway is not the bottleneck. Throughput (128 concurrent, roughly 31k req/s non-streaming and 10k req/s streaming) is reproduced by `scripts/bench.sh`; the first-byte benchmark by `scripts/bench-bigbody.sh`. Queue wait, upstream header time and first-content time are recorded per call so you can locate where real traffic spends its time.

## Tests

```bash
go test -race ./...   # unit and integration
make smoke            # end to end against a mock upstream, 56 checks
make crud             # full admin and user API round trip, 91 checks
```

## High availability

Run several instances on a shared PostgreSQL behind a load balancer. Share `YZAPI_JWT_SECRET` and copy `data/security/credential.key` to every instance (upstream keys are encrypted with it). Concurrency counters are per instance.

## Layout

```
cmd/yzapi           entry point
internal/gateway    data plane: auth, limits, routing, upstream, protocol conversion (convert/)
internal/api        admin REST API
internal/routing    smart routing engine
internal/compliance compliance engine
internal/pricing    price table and cost
internal/logstore   async logs, metering journal, hourly rollups
internal/essink     Elasticsearch body auditing
web/                React + Ant Design frontend
tools/mockupstream  local mock upstream
tools/loadgen       load generator
docs/               deployment, API contract, design and acceptance records (Chinese)
```

## Design decisions

- **No quota reservation.** Concurrency and quota are checked at admission, tokens are counted at completion.
- **No built-in OAuth pools for subscription accounts.** Only official API keys, encrypted at rest in the data directory.
- **Measure before optimising.** Every data-plane change is compared on first-byte and concurrency benchmarks.
- **Idempotent admin API.** Database unique indexes make repeated create requests safe; a committed write whose runtime reload failed returns 503 with `committed:true` and a one-click reload.

## Contributing

Issues and pull requests are welcome. Please run `go test -race ./...`, `make smoke` and `make crud` before submitting.

## License

[MIT](LICENSE)

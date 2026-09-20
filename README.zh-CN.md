<p align="center">
  <img src="web/public/favicon.svg" width="72" alt="YZ AI Gateway">
</p>

<h1 align="center">YZ AI Gateway</h1>

<p align="center">
  团队级 AI 统一接入与治理网关。一个 Base URL、一个 API Key，接入所有模型与所有编程工具。
</p>

<p align="center">
  <a href="#快速开始">快速开始</a> ·
  <a href="#接入编程工具">接入编程工具</a> ·
  <a href="docs/deploy.md">部署指南</a> ·
  <a href="docs/api.md">API 文档</a> ·
  <a href="README.md">English</a>
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white">
  <img alt="React" src="https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=black">
  <img alt="License" src="https://img.shields.io/badge/License-MIT-green">
  <img alt="Docker" src="https://img.shields.io/badge/Docker-linux%2Famd64%20%7C%20arm64-2496ED?logo=docker&logoColor=white">
</p>

---

YZ AI Gateway 把团队用到的所有大模型供应商收拢到一个入口后面。成员用统一的地址和自己的 Key 调用，管理员在一个后台里管账号、管权限、管额度、看用量、算成本。**单个 Go 二进制、前端内嵌、SQLite 开箱即用**，也可以切到 PostgreSQL 做多实例。

它专门为 **编程工具** 场景打磨过：Claude Code、Codex、OpenCode、Gemini CLI、Cline、Cursor 等客户端不需要任何改动就能直接指向网关；协议在网关内自动转换，上游只有 OpenAI 协议的账号也能给 Claude Code 用。

## 为什么需要它

- **一处配置，处处可用**：成员只记一个地址和一个 Key，供应商换了、Key 轮换了、模型下线了，客户端不用动。
- **成本看得见**：每一次调用按当时单价冻结成本，按用户、用户组、模型、供应商、客户端多维统计；内置参考价目表，可从 LiteLLM 等来源一键同步。
- **额度管得住**：用户组并发、单 Key 并发、每分钟请求 / Token 上限、月度 Token 配额、Key 有效期与模型白名单。
- **故障切得快**：模型组有序 failover，账号健康状态、自动冷却与半开探测，优先级加权重的灰度分流。
- **计量不丢数**：本地 journal 先落盘再入库，重启回放，幂等去重，聚合可从明细重建并对账。
- **合规可审计**：敏感词多模式匹配加语义相似度审核，策略组按拦截 / 仅审计分级，可选 Elasticsearch 正文留档。

## 功能一览

| 模块 | 能力 |
|---|---|
| 统一接入 | `/v1/chat/completions`、`/v1/responses`、`/v1/messages`、`/v1/embeddings`、`/v1/images/generations`、`/v1/models`、Gemini 原生 `/v1beta/models/*`，以及非对话模型的自定义 JSON 接口 `POST /v1/<路径>`（分类、重排、TypeSafe Jev 等）；Bearer、`x-api-key`、`x-goog-api-key` 认证 |
| 协议转换 | OpenAI Chat、OpenAI Responses、Anthropic Messages、Gemini 四种协议两两互转，含流式、工具调用、图片、思考内容与预算映射；同协议请求原样直传 |
| 账号池 | 28 家内置供应商预设，多家国内供应商各带独立的 Anthropic 兼容入口；模型映射、发现模型、优先级、权重、并发上限、健康状态、自动冷却与半开探测、缓存命中自检 |
| 模型组 | 有序 failover，组名可直接当模型名调用 |
| 用户与用户组 | 席位、组并发、单 Key 并发、每分钟请求 / Token 上限、月度 Token 配额、授权模型组；Key 有效期与模型白名单 |
| 智能路由 | 虚拟模型名（默认 `yz-auto`）；上下文规则、本地规则、向量相似度三级判定；样本管理、决策预览、决策日志 |
| 内容合规 | 敏感词（Aho-Corasick）加审核样本语义匹配；策略组按拦截 / 仅审计与风险等级配置；审核日志；规则不可读时拒绝放行 |
| 计价与成本 | 内置参考价目表；从 LiteLLM、EasyCLIProxyAPI、自定义 URL 或文件预览后同步；缓存读 / 缓存写分段计价；美元账本、显示币种可切换 |
| 观测 | 概览实时监控、调用日志（每次账号尝试链路、用量状态、排队时间、上游响应头时间、首内容时间、客户端标签）、多维用量报表、`/metrics` Prometheus 指标 |
| 配置版本 | 账号、模型组、价目、设置每次改动前自动快照，可查看、打点、一键回滚 |
| 用户中心 | 模型广场、接入指引（各工具可复制配置）、API Key 管理、个人用量与日志 |
| 审计 | 可选 Elasticsearch 8.x / 9.x 请求 / 响应正文审计，带大小上限与保留天数 |

## 快速开始

### Docker

```bash
docker run -d --name yzapi --restart unless-stopped \
  -p 8080:8080 -v /opt/yzapi:/opt/yzapi \
  ghcr.io/liningbest/yzapi:latest
```

首次启动会打印一次性临时管理员密码：

```bash
docker logs yzapi 2>&1 | grep -A3 "initial administrator"
```

浏览器打开 `http://<服务器IP>:8080`，用 `admin` 和临时密码登录，系统会强制修改密码。也可以用 `-e YZAPI_INITIAL_ADMIN_PASSWORD='YourStrongPass123'` 显式指定。

### Docker Compose

```bash
docker compose up -d
```

加 `--profile postgres` 可以同时拉起 PostgreSQL，环境变量见 [docker-compose.yml](docker-compose.yml)。

### 二进制

从 [Releases](../../releases) 下载对应平台的包，解压后：

```bash
YZAPI_DATA_DIR=/opt/yzapi ./yzapi
```

systemd 单元文件见 [deploy/systemd](deploy/systemd)。

### 从源码构建

需要 Go 1.26+、Node 22+、pnpm。

```bash
make all   # 构建前端并编译到 bin/yzapi
make run   # 本地启动，数据目录 ./data
```

完整部署方案（1Panel 编排、镜像构建、多实例 PostgreSQL、备份与升级）见 [docs/deploy.md](docs/deploy.md)。

## 五步上线

1. **账号池** 添加上游账号：选供应商，填 API Key，配模型映射（请求模型名 → 上游模型名），设优先级与并发。保存时做一次最小调用验证。
2. **模型组** 把请求模型按顺序编组，作为授权与 failover 的单位。
3. **用户组** 设置并发、Token 配额、授权模型组。
4. **用户** 创建成员并归入用户组。
5. **设置 → 基础设置** 把接入地址改成成员实际访问网关的地址，例如 `https://gateway.example.com/v1`。

成员登录用户中心创建自己的 API Key，打开「接入指引」，按所用工具复制配置即可。

## 接入编程工具

用户中心的「接入指引」页会按管理员配置的地址生成下面这些片段。

**Claude Code**（`~/.claude/settings.json`）

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://gateway.example.com",
    "ANTHROPIC_AUTH_TOKEN": "sk-xxxx",
    "ANTHROPIC_MODEL": "claude-sonnet-4-5"
  }
}
```

**Codex**（`~/.codex/config.toml`）

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

**任何 OpenAI 兼容客户端 / SDK**

```bash
curl https://gateway.example.com/v1/chat/completions \
  -H "Authorization: Bearer sk-xxxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-chat","messages":[{"role":"user","content":"你好"}]}'
```

模型名容错解析（大小写、厂商前缀、日期后缀），`count_tokens`、CORS、`Retry-After` 透传，每个响应带 `X-Upstream-Account` / `X-Upstream-Model` / `X-Upstream-Protocol` 便于核对打到了哪里。跨协议转换的能力边界见 [docs/api.md](docs/api.md)。

## 支持的供应商

OpenAI、Anthropic、Google Gemini（原生与 OpenAI 兼容）、DeepSeek、阿里云百炼、腾讯云、火山方舟、智谱、Moonshot、MiniMax、阶跃星辰、百度千帆、硅基流动、xAI、Groq、Mistral、Together、Fireworks、Cerebras、OpenRouter、vLLM、Ollama、LM Studio、New API、TypeSafe（Jev，走自定义 JSON 接口），以及自定义 OpenAI 兼容 / Anthropic 兼容 / JSON 接口。

DeepSeek、Kimi、智谱、MiniMax、百炼各带独立的 Anthropic 兼容账号类型，Claude Code 可以同协议直连这些国产模型。

## 配置

进程级配置通过环境变量，运行时参数在管理端「设置」页调整。

| 变量 | 默认 | 说明 |
|---|---|---|
| `YZAPI_LISTEN` | `0.0.0.0:8080` | 监听地址 |
| `YZAPI_DATA_DIR` | `/opt/yzapi` | 数据目录（SQLite、凭据密钥、JWT 密钥、计量 journal） |
| `YZAPI_DB_DRIVER` | `sqlite` | `sqlite` 或 `postgres` |
| `YZAPI_DB_DSN` | | PostgreSQL DSN |
| `YZAPI_INITIAL_ADMIN_PASSWORD` | 随机 | 首次初始化的管理员密码，不写入日志 |
| `YZAPI_HTTP_PROXY` | | 强制所有上游请求走此代理；不设时遵循标准 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` |
| `YZAPI_UPSTREAM_HTTP2` | `1` | 设为 `0` 时对上游只用 HTTP/1.1，用于排查个别供应商流式不畅 |
| `YZAPI_JWT_SECRET` | 自动生成 | 多实例部署时需统一 |
| `YZAPI_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `YZAPI_METRICS_TOKEN` | 空 | 设置后 `/metrics` 需要 Bearer 认证 |
| `YZAPI_JOURNAL_FSYNC` | `interval` | 计量 journal 约每秒 fsync 一次；`always` 时每条记录返回前 fsync |
| `YZAPI_SHUTDOWN_TIMEOUT` | `60` | 优雅退出等待秒数 |

管理员密码重置：

```bash
yzapi -reset-password admin
```

## 智能路由

1. 添加一个 **向量** 类型账号，在「设置 → 向量服务」选中并测试连接。
2. 创建「简单」「复杂」两个文本模型组。
3. 「设置 → 智能路由」启用并填写虚拟模型名。
4. 「智能路由 → 样本管理」录入样本并构建向量，用「决策预览」验证。
5. 客户端用虚拟模型名请求，响应里的 `model` 字段为实际命中模型。

## 性能

网关在数据面上的附加开销，同协议 Anthropic 流式请求，零延迟 mock 上游，本机实测：

| 请求体 | 直连上游首字节 | 经网关首字节 | 网关多出 |
|---|---|---|---|
| 4 KB | 0.5 ms | 0.7 ms | 0.2 ms |
| 200 KB | 1.6 ms | 4.3 ms | 2.7 ms |
| 1 MB | 5.8 ms | 18.2 ms | 12.4 ms |

真实模型首字延迟以数百毫秒到数秒计，网关不会成为瓶颈。吞吐基准（128 并发，非流式约 31k req/s，流式约 10k req/s）见 `scripts/bench.sh`；首字节基准见 `scripts/bench-bigbody.sh`。调用日志里的排队时间、上游响应头时间、首内容时间三段计时可以直接定位真实流量的瓶颈在哪一段。

## 测试

```bash
go test -race ./...            # 单元与集成测试
make smoke                     # 端到端：mock 上游 + 网关，61 项
make crud                      # 管理 / 用户 API 全量往返，92 项
```

## 高可用

多实例共享 PostgreSQL，前置负载均衡。统一 `YZAPI_JWT_SECRET`，把 `data/security/credential.key` 复制到每个实例（上游 API Key 用它加密）。并发计数为实例内计数，多实例时按实例数折算上限。

## 目录结构

```
cmd/yzapi           入口
internal/gateway    数据面：鉴权、限流、路由、上游转发、协议转换（convert/）
internal/api        管理 REST API
internal/routing    智能路由引擎
internal/compliance 内容合规引擎
internal/pricing    价目表与成本计算
internal/logstore   异步日志、计量 journal 与小时级聚合
internal/essink     Elasticsearch 正文审计
web/                React + Ant Design 前端
tools/mockupstream  本地模拟上游
tools/loadgen       压测工具
docs/               部署指南、API 契约、性能建议、价目来源、版本说明
```

## 文档

- [部署指南](docs/deploy.md)：1Panel、Docker Compose、二进制 + systemd、多实例 PostgreSQL、备份与升级
- [API 契约](docs/api.md)：数据面与管理面全部接口、错误码、协议转换边界
- [Coding 场景性能建议](docs/coding-performance.md)：编程工具接入的配置建议与耗时口径
- [版本说明](docs/release-notes-1.0.3-1.0.4.md)：各版本变更与升级注意事项

## 设计取舍

- **不做配额预占**：并发与配额在请求准入时判断，Token 在结束时计数，避免预占带来的误拒。
- **不内置订阅账号 OAuth 池**：只对接官方 API Key，凭据在数据目录内加密存储。
- **先观测再优化**：数据面每次改动都以首字节与并发基准对照；本地基准里网关在 200 KB 会话体上的开销低于真实首字的 1%。
- **管理接口幂等**：创建类接口由数据库唯一索引保证重复提交不产生重复资源；写库成功但运行态刷新失败时返回 503 并明确告知已提交，提供一键重载。

## 贡献

欢迎 Issue 与 PR。提交前请跑通 `go test -race ./...`、`make smoke` 与 `make crud`。

## License

[MIT](LICENSE)

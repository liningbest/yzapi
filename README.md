# YZ AI Gateway

企业 / 团队级 AI 统一接入与治理网关。客户端只需配置一个 Base URL 和一个 API Key，即可通过标准 OpenAI / Anthropic 接口访问多家云模型与本地模型，并获得权限、并发、配额、智能路由、内容合规、调用日志与用量统计等治理能力。

单个 Go 二进制 + 内嵌前端，SQLite 开箱即用，PostgreSQL 可选。

## 功能

| 模块 | 能力 |
|---|---|
| 统一接入 | `/v1/chat/completions`、`/v1/responses`、`/v1/messages`、`/v1/embeddings`、`/v1/images/generations`、`/v1/models`；Bearer 与 `x-api-key` 认证 |
| 账号池 | 18 家内置供应商（OpenAI、Anthropic、DeepSeek、阿里云百炼、腾讯云、火山方舟、智谱、Moonshot、MiniMax、硅基流动、Gemini、xAI、OpenRouter、vLLM、Ollama、New API、自定义）；模型映射；发现模型；优先级、并发上限；健康状态与自动冷却 |
| 协议转换 | OpenAI Chat ↔ Anthropic Messages ↔ OpenAI Responses 三向转换，含流式、工具调用、思考内容；原生协议优先 |
| 模型组 | 有序 failover；可直接用组名当模型名调用 |
| 用户 / 用户组 | 席位管理、组并发、单 Key 并发、月度 Token 配额、授权模型组、默认组保护、唯一管理员保护 |
| 智能路由 | 虚拟模型名；上下文规则 + 本地规则 + 向量相似度三级判定；样本管理 / 决策预览 / 决策日志 / 统计 |
| 内容合规 | 敏感词（Aho-Corasick 多模式匹配）+ 审核样本语义匹配；策略组（拦截 / 仅审计 + 风险等级）；审核日志 |
| 观测 | 概览实时监控、调用日志（含每次账号尝试链路与用量状态）、多维用量统计、小时级预聚合、日志保留清理、`/metrics` Prometheus 指标 |
| 计量 | 本地 journal 先落盘再事务入库，重启回放、幂等去重；每条调用标记用量状态（已确认 / 部分 / 未知 / 无消耗）；聚合可从明细重建并对账 |
| 审计 | 可选 Elasticsearch 8.x / 9.x 正文审计（请求 / 响应正文、大小上限、保留天数） |
| 用户中心 | 模型广场、API Key 管理（仅创建时显示一次）、个人用量与日志 |
| 客户端兼容 | Claude Code、Codex、OpenCode、Cline 等直接接入：模型名容错解析（大小写、厂商前缀、日期后缀）、账号级透传未映射模型、`count_tokens`、CORS、`Retry-After` 透传；跨协议转换能力边界见 docs/api.md |

## 快速开始

完整部署方案（1Panel 编排、Docker Compose、二进制 + systemd、多实例 PostgreSQL、备份与升级）见 [docs/deploy.md](docs/deploy.md)。

### Docker

```bash
docker run -d --name yzapi --restart unless-stopped \
  -p 8080:8080 -v /opt/yzapi:/opt/yzapi \
  yzapi/gateway
```

首次启动会打印一次性临时密码：

```bash
docker logs yzapi 2>&1 | grep -A3 "initial administrator"
```

浏览器打开 `http://<服务器IP>:8080`，用 `admin` 和临时密码登录，系统会强制修改密码。

也可以显式指定初始密码：`-e YZAPI_INITIAL_ADMIN_PASSWORD='YourStrongPass123'`。

### Docker Compose

```bash
docker compose up -d
```

### 从源码构建

需要 Go 1.26+、Node 22+、pnpm。

```bash
make all          # 构建前端并编译二进制到 bin/yzapi
make run          # 本地启动（数据目录 ./data）
```

## 配置

进程级配置通过环境变量；运行时参数在管理端「设置」页调整。

| 变量 | 默认 | 说明 |
|---|---|---|
| `YZAPI_LISTEN` | `0.0.0.0:8080` | 监听地址 |
| `YZAPI_DATA_DIR` | `/opt/yzapi` | 数据目录（SQLite、凭据密钥、JWT 密钥） |
| `YZAPI_DB_DRIVER` | `sqlite` | `sqlite` 或 `postgres` |
| `YZAPI_DB_DSN` | | PostgreSQL DSN，如 `host=... user=... password=... dbname=... sslmode=disable` |
| `YZAPI_INITIAL_ADMIN_PASSWORD` | 随机 | 首次初始化的管理员密码，不写入日志 |
| `YZAPI_HTTP_PROXY` | | 上游请求代理，如 `http://127.0.0.1:7890` |
| `YZAPI_JWT_SECRET` | 自动生成 | 多实例部署时需统一 |
| `YZAPI_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `YZAPI_METRICS_TOKEN` | 空 | 设置后 `/metrics` 需要 Bearer 认证 |
| `YZAPI_JOURNAL_FSYNC` | `interval` | 计量 journal 默认约每秒 fsync 一次；`always` 时每条记录返回前 fsync（吞吐下降）。fsync 失败会保留待同步标记并计入 `sync_failures`，并非无条件承诺 |
| `YZAPI_SHUTDOWN_TIMEOUT` | `60` | 优雅退出等待秒数 |

管理员密码重置（容器内或二进制）：

```bash
yzapi -reset-password admin
```

## 使用流程

1. **账号池** 添加上游账号：选供应商 → 填 API Key → 配模型映射（请求模型名 → 上游模型名）→ 设优先级与并发。保存时会做一次最小调用验证。
2. **模型组** 把请求模型按顺序编组，供授权与智能路由使用。
3. **用户组** 设置并发、Token 配额与授权模型组；未选模型组表示可用全部模型。
4. **用户** 创建普通用户并归入用户组。
5. 用户登录 **用户中心** 创建 API Key，把 `http://<网关>/v1` 作为 Base URL 配到任意 OpenAI 兼容客户端。

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-xxxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-chat","messages":[{"role":"user","content":"你好"}]}'
```

Claude Code / Anthropic SDK 直接指向网关即可：

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_AUTH_TOKEN=sk-xxxx
```

即使上游只有 OpenAI 协议账号，网关也会自动完成 Anthropic ↔ OpenAI 的协议转换。

### 智能路由

1. 添加一个 **向量** 类型账号，在「设置 → 向量服务」选中并测试连接。
2. 创建「简单」「复杂」两个文本模型组。
3. 「设置 → 智能路由」启用并填写虚拟模型名（默认 `yz-auto`）。
4. 「智能路由 → 样本管理」录入样本并构建向量，用「决策预览」验证。
5. 客户端用虚拟模型名请求，网关自动分流；响应里的 `model` 字段为实际命中模型。

## 性能

Apple Silicon 单机，mock 上游零延迟，128 并发 5000 请求：

| 场景 | 吞吐 | p50 | p99 |
|---|---|---|---|
| 直连上游（非流式） | 63k req/s | 1.5 ms | 8.5 ms |
| 经网关（非流式） | 31k req/s | 3.7 ms | 11 ms |
| 直连上游（流式） | 32k req/s | 2.6 ms | 15 ms |
| 经网关（流式） | 10.5k req/s | 8.6 ms | 81 ms |

网关单请求附加开销约 2 ms，日志异步批量写入无丢失。真实场景上游延迟以百毫秒到数秒计，网关不会成为瓶颈。

复现：`./scripts/bench.sh`。

## 测试

```bash
go test ./...          # 单元测试
./scripts/smoke.sh     # 端到端：启动 mock 上游 + 网关，跑 47 项检查
python3 scripts/api-crud.py   # 管理 / 用户 API 全量往返检查：每个资源的创建、编辑、开关、删除与约束（83 项）
```

## 高可用

多实例共享 PostgreSQL，前置负载均衡；设置统一的 `YZAPI_JWT_SECRET`，并把 `data/security/credential.key` 复制到每个实例（上游 API Key 用它加密）。并发计数为实例内计数，多实例时按实例数折算上限。

## 目录结构

```
cmd/yzapi           入口
internal/gateway    数据面：鉴权、限流、路由、上游转发、协议转换（convert/）
internal/api        管理 REST API
internal/routing    智能路由引擎
internal/compliance 内容合规引擎
internal/logstore   异步日志与小时级聚合
internal/essink     Elasticsearch 正文审计
web/                React + Ant Design 前端
tools/mockupstream  本地模拟上游
tools/loadgen       压测工具
docs/api.md         管理 API 契约
```

## License

MIT

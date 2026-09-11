# Coding 场景性能建议

日期：2026-09-11。只建议、不改代码。面向 Claude Code / Codex / Cline / Gemini CLI：关心的是首字延迟（TTFT）和流式顺滑，不是压测吞吐。

对照过 `internal/gateway/handler.go`、`upstream.go`、`convert/`、默认设置、`scripts/bench.sh`。没有重新压测。若要验证，用真实会话大小打一条流式请求，看 `first_byte_ms`，不要只用 `scripts/bench.sh`。

## 结论

先配成薄转发，再考虑改热路径。

合规语义和智能路由默认关着。同协议直连时，网关在 mock 上大约多 2 ms（非流）/ 6 ms（流 p50）。真实模型是百毫秒到数秒。Coding 体感慢，多半是：跨协议转换、大请求体整包 JSON 重编码、失败重试、或者合规/向量被打开。不是 SSE 逐 token 转发本身。

三件事优先：

1. Claude Code 用 Anthropic 入口账号（各客户端对原生协议）。
2. 合规语义 / `yz-auto` 不要走 coding 流量。
3. 用日志里的 `first_byte_ms` 区分网关前置 vs 上游首字。

## 现在就能做的（不用改代码）

| 优先级 | 做什么 | 为什么 |
| --- | --- | --- |
| 1 | 每个客户端用上游的原生协议账号：Claude Code → Anthropic 兼容入口；Codex → OpenAI；Gemini CLI → 原生 Gemini | 同协议只改模型名再转发。跨协议要整包转换，流式还要逐事件 Unmarshal/Marshal；Anthropic ↔ Responses / Gemini 会 `chainStream` 两跳 |
| 2 | 保持「内容合规」关闭；不要给 coding 请求走虚拟模型 `yz-auto` | 语义审核和智能路由都要打向量服务。Coding 提示词通常 >8KB，进不了嵌入缓存，每次请求多一次嵌入 RTT（上限 `vector_timeout_sec`，默认 10s） |
| 3 | 账号优先级拉开，MaxRetries 不要靠太大；冷却中的号不要还排在最前 | 默认最多 3 次尝试。慢超时或 5xx 会把整段 RequestTimeout 吃掉再切号。Coding 体感上这比 JSON 解析贵得多 |
| 4 | 不要设 `YZAPI_HTTP_PROXY` 去打公网模型；`YZAPI_JOURNAL_FSYNC` 保持 `interval`；Elasticsearch 正文审计关掉或把 RequestKB 压小 | 强制代理给每跳加延迟。`always` fsync 伤吞吐不伤 TTFT。正文审计会给响应套 `capWriter`，多拷一份流 |
| 5 | 个别供应商流式卡顿时再试 `YZAPI_UPSTREAM_HTTP2=0` | 默认 HTTP/2 连接池（每 host 256 idle）。有的上游 HTTP/2 会把 SSE 攒包，首字或逐 token 变钝，这是逃生开关 |
| 6 | 用日志里的 `first_byte_ms` / `client_write_ms` / `X-Upstream-*` 判断瓶颈 | `first_byte_ms` 接近直连上游 → 网关前置可忽略。`client_write_ms` 高 → 客户端或反向代理在缓冲，不是网关 CPU |

## 延迟实际加在哪

Claude Code 流式、同协议 Anthropic：鉴权 → 整包读 body → JSON 解析 → 限流配额 →（可选合规/路由）→ 改模型名再 Marshal → 上游。上游头回来之后按 SSE 事件 Flush。计量 journal 在流结束后才写，不挡首字。

| 阶段 | 同协议 | 跨协议 | 对 Coding 的影响 |
| --- | --- | --- | --- |
| 请求进网关到发上游之前 | `ReadAll` + Unmarshal + 整表再 Marshal（即使只改 `model`） | 再加 Anthropic ↔ Chat（↔ Gemini / Responses）整包转换 | 会话上下文到几百 KB 时，这是网关真正能加上去的 TTFT。`bench.sh` 用的是小 body，测不到 |
| 上游首字 | HTTP/2 复用，头到了立刻 `WriteHeader` + Flush | 同左，转换不攒整段回复 | 通常是大头。网关已经关了响应压缩（`Accept-Encoding: identity`）和 nginx 缓冲头 |
| 逐 token | 解析 SSE 事件、抽 usage、再写出并 Flush | 每个 delta 再 JSON 编解码；两跳转换走 pipe | 同协议几乎感觉不到。跨协议在高并发下会拉高 p99，单人 coding 仍远小于模型间隔 |
| 流结束 | journal 异步、计价、聚合 | 同左 | 不影响 TTFT。`YZAPI_JOURNAL_FSYNC=always` 才会拖提交 |

同协议整包再 Marshal 在 `internal/gateway/handler.go` 的 `buildBody`（约 977–989 行）：即使只改模型名也会把整份 raw map 再编码一遍。Gemini 直连已经原样传 `req.body`。

## 值得以后改的热路径

- 同协议且模型名已是上游名、又不必注入 `stream_options` 时，直接传 `req.body`。现在 `buildBody` 即使只改一个字段也会把整份 raw map Marshal 一遍。
- `extractText`（合规/路由用）会再解析一遍 `messages`。coding 网关若永不开这两项，可以短路掉。
- 再往后才是：同协议尽量不要把整包 JSON 读进内存再发给上游（要动鉴权/审核的设计）。那是超大上下文才值得做的。
- SSE 的 `WriteSSE` 每次 new Buffer、转换路径逐 token Marshal，属于微优化。单人 coding 排在整包 JSON 和失败重试后面。

## 先别优化这些

- gin 包一层 `WrapF`、CAS 并发槽、配额读锁、atomic 快照，相对大 body 和上游都可以忽略。
- README 里 128 并发 5000 次的吞吐数字，是零延迟 mock + 小 OpenAI 请求。不能代表 Claude Code 大上下文流式。
- 多实例进程内限流会影响配额准确性，几乎不影响单请求延迟。
- 设置里 MaxConcurrency 512、Queue 1024 对几路 IDE 足够。排队变多时先查上游慢和账号冷却，不要先加队列。

## 怎么确认网关不是瓶颈

| 你看到的 | 含义 |
| --- | --- |
| `first_byte_ms` 和直连该供应商差不多 | 前置处理可接受，去查模型和网络 |
| `first_byte_ms` 明显更大，body 很大 | `ReadAll` + 再 Marshal 或跨协议转换；对照是否同协议 |
| `first_byte_ms` 更大，且开了合规/智能路由 | 嵌入调用在发上游之前，优先关掉 |
| attempts 里先有一次超时/5xx 再成功 | 重试在吃 TTFT，调优先级和冷却 |
| `client_write_ms` 高、upstream 正常 | IDE、本机代理或前面的 nginx 在攒 SSE |

## 建议的配置画像

一台专门给 coding 用的网关：合规关、智能路由关、正文审计关；Claude / GPT / Gemini 各用原生协议账号；映射用精确模型名；重试 1–2 次；journal 默认 `interval`。这样网关就是鉴权、选号、计量，剩下的时间交给模型。

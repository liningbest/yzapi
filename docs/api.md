# YZ AI Gateway — 管理 API 契约

所有管理接口在 `/api` 下，JSON 请求 / 响应。认证使用 `Authorization: Bearer <jwt>`。

统一错误格式：`{"error": "message", "code": "snake_case_code"}`，HTTP 状态码 4xx/5xx。

列表接口统一：查询参数 `page`（从 1 开始）、`page_size`（默认 20，最大 200），响应 `{"items": [...], "total": N}`。
时间范围参数 `range`: `24h` | `7d` | `30d` | `custom`（配合 `from`、`to`，RFC3339）。

数据面接口（客户端调用，API Key 认证）：`GET /v1/models`、`GET /v1/models/{id}`、`POST /v1/chat/completions`、`POST /v1/responses`、`POST /v1/messages`、`POST /v1/messages/count_tokens`、`POST /v1/embeddings`、`POST /v1/images/generations`。为兼容各种编程客户端：

- 认证头接受 `Authorization: Bearer`、`x-api-key`、`api-key` 三种；路径省略或重复 `/v1` 也接受；`/v1/*` 支持 CORS 预检，浏览器内的客户端可直连。
- **模型名解析**：客户端发来的模型名依次按精确、忽略大小写、去掉厂商前缀（`anthropic/`、`openai/`、`models/` 等）、去掉 `-latest`、版本 / 日期后缀容错（`claude-sonnet-4-5-20250929` 命中映射 `claude-sonnet-4-5`，反之亦可；只接受数字或日期形态的后缀，`gpt-5-codex` 不会命中 `gpt-5`）匹配映射；日志与报表记录解析后的名称。**歧义**（多个同长候选，或仅大小写不同的多个映射）返回 400 `model_ambiguous`，不会调用上游，也不会落入透传。
- **透传未映射模型**：账号开启 `passthrough_models` 后，没有映射的模型名原样转发给该账号（按账号类型限定文本 / 向量 / 文生图）。显式映射优先；用户组绑定了模型组时不允许透传。
- `POST /v1/messages/count_tokens`：与生成请求走同一前半段（鉴权、正文上限与内存预算、模型解析、用户组与模型组授权、全局 / 组 / Key 并发槽），因此受同样的性能设置约束；有 Anthropic 协议账号可服务该模型时原样转发（改写模型名、透传 `anthropic-beta`），否则按请求体大小估算并带 `X-Token-Count-Estimated: true`。估算只用于上下文辅助，不是计量值，不进入用量账本。
- `GET /v1/models` / `GET /v1/models/{id}` 每项同时带 OpenAI 字段（`object:"model"/created/owned_by`）与 Anthropic ModelInfo 字段（`type:"model"/display_name/created_at`），列表含 `has_more`；网关自身分类放在 `model_type`（text / embedding / image），`kind` 为 model / virtual / group。透传账号可服务的未映射名称在详情查询中同样返回可用。
- 最终失败为 429 时把上游的 `Retry-After` 透传给客户端。

**跨协议转换的能力边界**（客户端协议与上游协议不同时才会发生；同协议直连时只改写模型名，其余字段原样透传）：

| 内容 | Chat ↔ Messages | Responses ↔ Chat | 说明 |
|---|---|---|---|
| 文本、系统提示、多轮消息 | 保留 | 保留 | |
| 工具定义、工具调用、工具结果、调用 ID | 保留 | 保留 | 流式参数分片会重组 |
| 图片（base64 / URL） | 保留 | 保留 | |
| thinking / reasoning 内容 | 保留文本 | 保留摘要 | 供应商签名（`signature`）无法跨协议映射 |
| `cache_control` 提示缓存标记 | 丢失 | 丢失 | 只有同协议直连到 Anthropic 才生效 |
| Responses 有状态字段 `previous_response_id` | 不适用 | 拒绝，400 | 需要原生 Responses 上游 |
| `metadata`、`service_tier`、`prompt_cache_key` 等扩展字段 | 丢失 | 丢失 | 同协议直连保留 |
| usage 计量 | 保留 | 保留 | |

建议：给每个供应商账号开启它原生支持的协议，让客户端命中同协议路径；转换只作为兜底。

## 公共

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /health/live | `{"status":"ok"}` |
| GET | /health/ready | `{"status":"ok"}` 或 503 `{"status":"not_ready"}` |
| GET | /api/public/info | `{site_name, version, base_url}` |
| GET | /metrics | Prometheus 文本格式指标；设置 `YZAPI_METRICS_TOKEN` 后需 `Authorization: Bearer <token>` |

## 认证

| 方法 | 路径 | 请求 | 响应 |
|---|---|---|---|
| POST | /api/auth/login | `{username, password}` | `{token, user}`；用户被锁定返回 423；密码错误 401 |
| POST | /api/auth/logout | | `{}` |
| GET | /api/auth/me | | `user` |
| POST | /api/auth/change-password | `{old_password, new_password}` | `{}`；成功后旧 token 失效需重新登录 |

`user` 对象：`{id, username, role: "admin"|"user", group_id, group_name, enabled, locked, must_change_password, note, last_login_at, created_at}`

## 管理端 `/api/admin/*`（仅 admin）

### 概览
- `GET /api/admin/overview/live` → `{active_users, streams, inflight, limit, waiting, queue_size, accounts:[{id,name,provider,priority,current,limit,util,health,cooldown_until,last_error}], groups:[{id,name,current,limit,util,tokens_used,token_quota}]}`
- `GET /api/admin/overview/usage?range=24h` → `{tokens:{total,prompt,completion,cached,cache_rate}, requests:{total,success,failed,fail_rate}, active_users, active_keys, trend:[{time, total_tokens, prompt_tokens, completion_tokens, cached_tokens, requests}]}`

### 供应商与模型
- `GET /api/admin/providers` → `[{key,name,base_url,types[],protocols[],account_types:[{key,name,base_url,protocols[]}],auth_header,discover,custom,icon}]`。`account_types[].protocols` 存在时表示该入口只讲这些协议（例如 DeepSeek / Kimi / 智谱 / MiniMax / 百炼 的 Anthropic 兼容入口只讲 `anthropic-messages`），创建账号时协议默认与校验都以它为准，避免把 OpenAI 与 Anthropic 协议混在同一个 Base URL 下
- `GET /api/admin/models` → `[{name,type,kind:"model"|"virtual"|"group",provider,accounts,models[]}]`（当前可路由的全部请求模型）

### 账号池 `/api/admin/accounts`
账号对象：
```
{id, name, provider, account_type, type:"text"|"image"|"embedding", base_url, has_key:true, api_key_masked:"sk-ab…yz",
 protocols:["openai-completions",...], mappings:[{id,request_model,upstream_model}], test_model, priority, weight, max_concurrency,
 enabled, health:"available"|"cooling"|"unavailable", cooldown_until, last_error, note, created_at, updated_at}
```
- `GET /api/admin/accounts?provider=&type=&protocol=&enabled=&health=&q=&page=&page_size=`
- `POST /api/admin/accounts` body: `{name, provider, account_type, type, base_url, api_key, protocols[], mappings:[{request_model,upstream_model}], test_model, priority, max_concurrency, enabled, note, skip_test:false}` → 账号对象。保存前会做一次最小调用验证（文本 / 向量），失败返回 400 `{error, code:"validation_failed"}`。
- `GET /api/admin/accounts/:id`
- `PUT /api/admin/accounts/:id` 同 POST；`api_key` 为空表示沿用。`type` 不可修改。
- 调度顺序：先按 `priority` 从小到大分层，同一层内按 `weight`（1–1000，默认 1）加权随机排序，因此权重 5 与 95 并列即 5% 灰度。账号冷却到期后进入半开状态：只放行一个探测请求，成功即恢复，失败则按退避延长冷却，其余请求在探测期间仍视该账号不可用。
- 数据面成功响应带 `X-Upstream-Account` / `X-Upstream-Model` / `X-Upstream-Protocol` 头，指明实际服务的账号、上游模型与协议。
- `DELETE /api/admin/accounts/:id`；被向量服务引用时返回 409。
- `PATCH /api/admin/accounts/:id/enabled` `{enabled}`
- `POST /api/admin/accounts/:id/reset-health` → 清除冷却
- `POST /api/admin/accounts/:id/test-model` `{model}` → `{ok, latency_ms, message, model}`，用已存凭据探测指定上游模型
- `PUT /api/admin/accounts/:id/mappings` `{mappings:[{request_model,upstream_model}]}` → 账号对象；整体替换映射（1–100 条，请求模型名唯一）
- `POST /api/admin/accounts/discover` `{provider, base_url, api_key, account_id}` → `{models:["..."]}`（account_id 提供且 api_key 为空时用已存 key）
- `POST /api/admin/accounts/test` 同 POST 账号 body（可带 `account_id`）→ `{ok, latency_ms, message, model}`

### 模型组 `/api/admin/model-groups`
对象：`{id, name, type, models:["a","b"], note, created_at, updated_at, in_use_by_route:bool, route_roles:["simple"|"complex"]}`
- `GET`（支持 `type=`、`q=`）、`POST`、`GET /:id`、`PUT /:id`、`DELETE /:id`（被智能路由引用返回 409）

### 用户 `/api/admin/users`
- `GET ?role=&group_id=&enabled=&q=&page=`；每项为 `user` 对象 + `api_keys_count` + `is_last_admin`（服务端按全部已启用管理员计算，前端据此禁用最后一个管理员的停用 / 降级 / 删除）
- `POST {username, password, group_id, role:"user"|"admin", note}`
- `PUT /:id {group_id, role, note}`
- `DELETE /:id`
- `PATCH /:id/enabled {enabled}`
- `POST /:id/reset-password {password}`
- `POST /:id/unlock`
唯一管理员保护：对最后一个 admin 的禁用 / 降级 / 删除返回 409 `code:"last_admin"`。

### 用户组 `/api/admin/user-groups`
对象：`{id, name, max_concurrency, key_max_concurrency, token_quota, is_default, enabled, note, model_group_ids:[...], model_groups:[{id,name}], members_count, tokens_used_month, created_at}`
- `GET`、`POST {name,max_concurrency,key_max_concurrency,token_quota,model_group_ids[],enabled,note}`、`PUT /:id`、`DELETE /:id`（有成员 409 `has_members`；默认组 409 `is_default`）、`PATCH /:id/enabled`

### 智能路由 `/api/admin/route`
- 样本：`GET /samples?label=&q=&vectorized=true|false&page=`；对象 `{id,label:"simple"|"complex",text,threshold,note,vector_dim,vectorized:bool,created_at}`
- `POST /samples {label,text,threshold,note,build_vector:true}`；`PUT /samples/:id`（文本变化且 `build_vector` 为 true 时同步重建向量，失败时响应附 `build_error`）；`DELETE /samples/:id`
- `POST /samples/batch {items:[{label,text,note}], build_vector:true}` → `{created:N}`
- `POST /samples/build {ids:[1,2]}` 或 `{all:true}` → `{built:N, failed:N, error}`
- `POST /preview {text}` → `{label, source, confidence, group_id, group_name, models:[...], top_k:[{id,label,text,score}], normalized, latency_ms}`
- 决策日志：`GET /decisions?label=&source=&request_type=&model=&q=&range=&page=`；对象 `{id,request_id,label,source,confidence,selected_model,model_group,normalized_text,top_k:[...],request_type,total_tokens,latency_ms,failed,created_at}`；`GET /decisions/:id`
- `GET /stats?range=24h` → `{decisions, requests, failed, total_tokens, avg_tokens, latency_ms, by_label:[{key,count,tokens}], by_source:[...], by_model:[...], by_token_bucket:[{key,count}]}`

### 内容合规 `/api/admin/compliance`
- 策略组 `/policy-groups`：对象 `{id,name,action:"block"|"audit",risk_level:"low"|"medium"|"high",enabled,description,words_count,samples_count,created_at}`；`GET`、`POST`、`PUT /:id`、`DELETE /:id`（被引用 409）、`PATCH /:id/enabled`
- 敏感词 `/words`：对象 `{id,policy_group_id,policy_group:{id,name,action,risk_level},word,note,enabled,created_at}`；`GET ?policy_group_id=&enabled=&q=&page=`、`POST`、`PUT /:id`、`DELETE /:id`、`PATCH /:id/enabled`、`POST /words/batch {policy_group_id, words:["a","b"]}` → `{created}`
- 审核样本 `/samples`：对象 `{id,policy_group_id,policy_group,text,note,enabled,vector_dim,vectorized,created_at}`；`GET`、`POST {policy_group_id,text,note,enabled,build_vector}`、`PUT /:id`、`DELETE /:id`、`POST /samples/build {ids|all}`
- 审核日志 `/audit-logs`：`GET ?action=&risk_level=&detect_method=&policy_group_id=&q=&range=&page=`；对象 `{id,request_id,user_id,username,request_model,protocol,action,risk_level,detect_method,policy_group_id,policy_group,evidence,confidence,status_code,hits:[{method,policy_group,evidence,score,action,risk_level}],snippet,created_at}`；`GET /audit-logs/:id`
- `POST /test {text}` → 判定结果（同 hits 结构 + `hit`, `block`）

### 调用日志 `/api/admin/logs`
- `GET ?user_id=&group_id=&account_id=&provider=&api_type=&result=&status_code=&model=&q=&range=&from=&to=&page=`
- 对象：`{id,request_id,user_id,username,group_id,group_name,api_key_id,api_key_name,account_id,account_name,provider,request_model,upstream_model,model_group,api_type,client_protocol,upstream_protocol,stream,prompt_tokens,completion_tokens,total_tokens,cached_tokens,tokens_known,result:"success"|"client_error"|"upstream_error"|"blocked"|"rate_limited",status_code,latency_ms,upstream_latency_ms,first_byte_ms,client_write_ms,error,attempts:[{account_id,account_name,provider,protocol,model,status_code,latency_ms,error}],route_label,client_ip,created_at}`
- `GET /:id`
- `GET /api/admin/logs/filters` → `{users:[{id,username}], accounts:[{id,name,provider}], providers:[...], models:[...]}`
- 耗时字段口径：`latency_ms` 整个请求；`first_byte_ms` 收到上游响应头；`upstream_latency_ms` 从发往上游到转发结束（流式时包含交错的客户端写入）；`client_write_ms` 流式时向客户端写入与刷新被阻塞的时间。`upstream_latency_ms − client_write_ms` 约等于纯上游等待；`client_write_ms` 偏大说明客户端或反向代理接收慢。这些数与客户端侧测得的总耗时之间还隔着客户端连接与网络传输，不能直接相减归责。
- 日志对象另含 `usage_status`（`confirmed` 上游返回了完整 usage；`partial` 流中断、只拿到部分 usage；`unknown` 上游处理了请求但未返回 usage；`none` 请求未被任何上游处理）与 `est_prompt_tokens`（非 confirmed 时按请求体字节 / 4 的粗略估算，仅供参考，不是下限；含图片或元数据时偏差大）。`attempts[]` 每次尝试带 `usage_status`、`prompt_tokens`、`completion_tokens`。`usage_corrected` 为 true 表示该请求由旧版本网关写入时只记了最后一次尝试的用量，升级或重建时已按各次尝试记录重新汇总（与当前网关对实时请求的折叠规则相同），原始尝试记录未改动。

### 用量统计 `/api/admin/usage`
- `GET ?user_id=&group_id=&account_id=&provider=&api_type=&model=&api_key_id=&range=&from=&to=&group_by=model|api_key`
- 响应：
```
{summary:{requests,success,failed,prompt_tokens,completion_tokens,total_tokens,cached_tokens},
 trend:[{time, requests, total_tokens, series:{"<model or key name>": tokens}}],
 by_provider:[{key,name,requests,total_tokens,cached_tokens}], by_model:[...], by_model_group:[...],
 by_account:[...], by_group:[...], by_user:[...], by_api_key:[...]}
```
`key` 为 id 或名称，`name` 为显示名；已删除资源 name 追加 `(已删除)`。

### 计量维护 `/api/admin/usage`
- 用量响应的 `summary` 与各分布项含 `unknown_usage`（用量为 partial / unknown 的请求数）；各分布项另含 `attempts`。归属规则：`requests` / 成功失败 / 延迟按最终应答的账号记，Token 按实际消耗它的那次上游尝试的账号与供应商记，因此跨账号重试的一次请求会把 Token 拆到多行，但请求数只计一次；用户、用户组、API Key 的 Token 总量始终等于请求总量。小时表 `usage_hourlies` 同样带 `attempts` 列，常规入库与重建使用同一聚合函数。升级兼容：首次以新版本启动时一次性回填 NULL 的 `attempts`，并把保留期内、明细仍完整的小时按新口径重建（记录在 `metering.attempts_since`）；早于该时间的小时没有记录过尝试次数，`attempts` 为 0 表示"未记录"而非真实零尝试。累加语句对 NULL 兼容。旧版本少计的请求（请求级只有最后一次尝试）在重建或 journal 回放入库时按尝试记录修正并标记 `usage_corrected`，重复处理不会重复计量。请求级 `total_tokens` 超出各次尝试输入 + 输出之和的部分（供应商报告在输入输出之外的 Token，修正时予以保留）记在最终应答账号行的 `total_tokens` 上，不伪装成输入或输出；因此聚合的 `total_tokens` 恒等于请求的 `total_tokens`，对账以此为准。聚合口径变化时升级标记带版本号（当前 3），旧版本标记的库会在启动时把明细仍完整的区间再重建一次；已清理的区间不会被触碰。
- `POST /api/admin/usage/rebuild {from, to}`（RFC3339，最多 92 天；`from` 不得早于调用日志保留期，也不得早于持久化的明细清理边界 `purged_before`，否则 400 `outside_retention`，避免用已清理的明细抹掉历史聚合。调大保留期不会恢复已删除的明细，因此边界只前进不后退。边界读取失败或格式错误时重建直接报错且不改动聚合；边界校验在重建事务内进行，并与清理协程互斥）→ `{hours, rows}`：从原始调用日志重建小时聚合。
- `GET /api/admin/usage/reconcile?range=` → `{consistent, mismatches:[{hour, log_requests, rollup_requests, log_tokens, rollup_tokens}]}`：逐小时对账。
- `GET /api/admin/usage/metering` → `{pending_bytes, overflow_records, dirty, dropped, replayed, write_failures, sync_failures, last_commit_at, purged_before, attempts_since}`：计量 journal 状态。`overflow_records` > 0 或 `dirty` 长期为 true、`sync_failures` 增长，都表示有记录尚未持久化。

计量可靠性（按条件说明，不做无条件承诺）：
- 写入成功：每次调用以直接 write(2) 追加到 `data/journal/calls.jsonl`，无用户态缓冲，`Record` 返回后记录已在内核页缓存，进程崩溃不丢。
- 同步成功：独立的同步协程约每秒 fsync 一次（不与入库、重试退避共用循环）；`YZAPI_JOURNAL_FSYNC=always` 时每条记录返回前 fsync。fsync 失败保留待同步标记并在下一轮重试，计入 `sync_failures`；设备故障时 `always` 也不能保证已到稳定存储。
- 故障降级：journal 不可写时记录只保留在内存溢出区（`overflow_records`），进程退出即丢失；溢出区满后丢弃并计入 `dropped`。
- 入库：后台事务性地写入 `call_logs` 并更新小时聚合，提交后推进检查点；重启回放未提交部分，`request_id` 唯一保证幂等。
- 请求级用量始终由各次上游尝试汇总：已知 Token 累加（含失败尝试与错误响应体报告的用量），任一尝试 unknown 则请求为 unknown，流被截断为 partial，只有未到达任何上游才是 none；客户端取消时按"请求是否已写到上游"区分 none / unknown。

### 设置 `/api/admin/settings`
- `GET` → `{basic, performance, vector, smart_route, compliance, elasticsearch}`（密钥字段脱敏为 `"******"`）
- `PUT /basic {base_url, log_retention_days, protocol_conversion, site_name}`
- `PUT /performance {max_concurrency, queue_size, queue_timeout_sec, request_timeout_sec, stream_idle_timeout_sec, max_body_kb, cooldown_sec, max_retries, upstream_connect_timeout_sec, max_body_memory_mb, vector_max_concurrency, vector_timeout_sec}`；所有字段不得为负（400），`max_retries / max_body_kb / max_body_memory_mb / vector_max_concurrency / vector_timeout_sec` 传 0 恢复默认值
- `PUT /vector {account_id, model}`；`POST /vector/test {account_id, model}` → `{ok, dim, latency_ms, message}`
- `PUT /smart_route {enabled, virtual_model, simple_group_id, complex_group_id, threshold, confidence_gap, top_k}`
- `PUT /compliance {enabled, semantic_threshold, check_system_prompt, on_failure:"allow"|"block"}`
- `PUT /elasticsearch {enabled,url,auth_type,api_key,username,password,index_prefix,request_kb,response_kb,retention_days}`；`POST /elasticsearch/test` → `{ok, version, message}`；`GET /elasticsearch/status` → `{configured, queue_count, queue_bytes, dropped, last_success_at, failing_since}`
- 密钥字段传 `"******"` 表示保持不变。

### 系统
- `GET /api/admin/system/info` → `{version, go_version, db_driver, uptime_sec, started_at, data_dir}`

## 用户中心 `/api/user/*`（任意登录用户）
- `GET /api/user/models` → `{base_url, models:[{name,type,kind,provider}]}`（仅本人用户组可用的模型）
- `GET /api/user/keys` → `[{id,name,prefix,suffix,masked:"sk-abcd…wxyz",enabled,last_used_at,created_at}]`
- `POST /api/user/keys {name}` → `{key:"sk-完整明文（仅此一次）", item:{...}}`
- `PUT /api/user/keys/:id {name}`；`PATCH /api/user/keys/:id/enabled {enabled}`；`DELETE /api/user/keys/:id`
- `GET /api/user/usage?api_key_id=&model=&api_type=&range=&from=&to=&group_by=` → 同管理端用量结构（仅含 by_model / by_api_key / by_provider）
- `GET /api/user/logs?api_key_id=&model=&result=&range=&q=&page=` → 同调用日志对象（脱敏，不含账号信息）
- `GET /api/user/group` → `{id,name,max_concurrency,key_max_concurrency,token_quota,tokens_used_month,model_groups:[{id,name,models[]}]}`

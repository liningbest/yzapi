# YZ AI Gateway — 管理 API 契约

所有管理接口在 `/api` 下，JSON 请求 / 响应。认证使用 `Authorization: Bearer <jwt>`。

统一错误格式：`{"error": "message", "code": "snake_case_code"}`，HTTP 状态码 4xx/5xx。

列表接口统一：查询参数 `page`（从 1 开始）、`page_size`（默认 20，最大 200），响应 `{"items": [...], "total": N}`。
时间范围参数 `range`: `24h` | `7d` | `30d` | `custom`（配合 `from`、`to`，RFC3339）。

数据面接口（客户端调用，API Key 认证）：`GET /v1/models`、`POST /v1/chat/completions`、`POST /v1/responses`、`POST /v1/messages`、`POST /v1/embeddings`、`POST /v1/images/generations`。

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
- `GET /api/admin/providers` → `[{key,name,base_url,types[],protocols[],account_types:[{key,name,base_url}],auth_header,discover,custom,icon}]`
- `GET /api/admin/models` → `[{name,type,kind:"model"|"virtual"|"group",provider,accounts,models[]}]`（当前可路由的全部请求模型）

### 账号池 `/api/admin/accounts`
账号对象：
```
{id, name, provider, account_type, type:"text"|"image"|"embedding", base_url, has_key:true, api_key_masked:"sk-ab…yz",
 protocols:["openai-completions",...], mappings:[{id,request_model,upstream_model}], test_model, priority, max_concurrency,
 enabled, health:"available"|"cooling"|"unavailable", cooldown_until, last_error, note, created_at, updated_at}
```
- `GET /api/admin/accounts?provider=&type=&protocol=&enabled=&health=&q=&page=&page_size=`
- `POST /api/admin/accounts` body: `{name, provider, account_type, type, base_url, api_key, protocols[], mappings:[{request_model,upstream_model}], test_model, priority, max_concurrency, enabled, note, skip_test:false}` → 账号对象。保存前会做一次最小调用验证（文本 / 向量），失败返回 400 `{error, code:"validation_failed"}`。
- `GET /api/admin/accounts/:id`
- `PUT /api/admin/accounts/:id` 同 POST；`api_key` 为空表示沿用。`type` 不可修改。
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
- `GET ?role=&group_id=&enabled=&q=&page=`；每项为 `user` 对象 + `api_keys_count`
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
- `POST /samples {label,text,threshold,note,build_vector:true}`；`PUT /samples/:id`；`DELETE /samples/:id`
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
- 对象：`{id,request_id,user_id,username,group_id,group_name,api_key_id,api_key_name,account_id,account_name,provider,request_model,upstream_model,model_group,api_type,client_protocol,upstream_protocol,stream,prompt_tokens,completion_tokens,total_tokens,cached_tokens,tokens_known,result:"success"|"client_error"|"upstream_error"|"blocked"|"rate_limited",status_code,latency_ms,upstream_latency_ms,first_byte_ms,error,attempts:[{account_id,account_name,provider,protocol,model,status_code,latency_ms,error}],route_label,client_ip,created_at}`
- `GET /:id`
- `GET /api/admin/logs/filters` → `{users:[{id,username}], accounts:[{id,name,provider}], providers:[...], models:[...]}`
- 日志对象另含 `usage_status`（`confirmed` 上游返回了完整 usage；`partial` 流中断、只拿到部分 usage；`unknown` 上游处理了请求但未返回 usage；`none` 请求未被任何上游处理）与 `est_prompt_tokens`（非 confirmed 时按请求体字节 / 4 的粗略估算，仅供参考，不是下限；含图片或元数据时偏差大）。`attempts[]` 每次尝试带 `usage_status`、`prompt_tokens`、`completion_tokens`。

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
- 用量响应的 `summary` 与各分布项含 `unknown_usage`（用量为 partial / unknown 的请求数）。
- `POST /api/admin/usage/rebuild {from, to}`（RFC3339，最多 92 天；`from` 不得早于调用日志保留期，也不得早于持久化的明细清理边界 `purged_before`，否则 400 `outside_retention`，避免用已清理的明细抹掉历史聚合。调大保留期不会恢复已删除的明细，因此边界只前进不后退）→ `{hours, rows}`：从原始调用日志重建小时聚合。
- `GET /api/admin/usage/reconcile?range=` → `{consistent, mismatches:[{hour, log_requests, rollup_requests, log_tokens, rollup_tokens}]}`：逐小时对账。
- `GET /api/admin/usage/metering` → `{pending_bytes, overflow_records, dirty, dropped, replayed, write_failures, sync_failures, last_commit_at, purged_before}`：计量 journal 状态。`overflow_records` > 0 或 `dirty` 长期为 true、`sync_failures` 增长，都表示有记录尚未持久化。

计量可靠性（按条件说明，不做无条件承诺）：
- 写入成功：每次调用以直接 write(2) 追加到 `data/journal/calls.jsonl`，无用户态缓冲，`Record` 返回后记录已在内核页缓存，进程崩溃不丢。
- 同步成功：独立的同步协程约每秒 fsync 一次（不与入库、重试退避共用循环）；`YZAPI_JOURNAL_FSYNC=always` 时每条记录返回前 fsync。fsync 失败保留待同步标记并在下一轮重试，计入 `sync_failures`；设备故障时 `always` 也不能保证已到稳定存储。
- 故障降级：journal 不可写时记录只保留在内存溢出区（`overflow_records`），进程退出即丢失；溢出区满后丢弃并计入 `dropped`。
- 入库：后台事务性地写入 `call_logs` 并更新小时聚合，提交后推进检查点；重启回放未提交部分，`request_id` 唯一保证幂等。
- 请求级用量始终由各次上游尝试汇总：已知 Token 累加（含失败尝试与错误响应体报告的用量），任一尝试 unknown 则请求为 unknown，流被截断为 partial，只有未到达任何上游才是 none；客户端取消时按"请求是否已写到上游"区分 none / unknown。

### 设置 `/api/admin/settings`
- `GET` → `{basic, performance, vector, smart_route, compliance, elasticsearch}`（密钥字段脱敏为 `"******"`）
- `PUT /basic {base_url, log_retention_days, protocol_conversion, site_name}`
- `PUT /performance {max_concurrency, queue_size, queue_timeout_sec, request_timeout_sec, stream_idle_timeout_sec, max_body_kb, cooldown_sec, max_retries, upstream_connect_timeout_sec, max_body_memory_mb, vector_max_concurrency, vector_timeout_sec}`
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

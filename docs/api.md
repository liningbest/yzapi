# YZ AI Gateway — 管理 API 契约

所有管理接口在 `/api` 下，JSON 请求 / 响应。认证使用 `Authorization: Bearer <jwt>`。

统一错误格式：`{"error": "message", "code": "snake_case_code"}`，HTTP 状态码 4xx/5xx。

列表接口统一：查询参数 `page`（从 1 开始）、`page_size`（默认 20，最大 200），响应 `{"items": [...], "total": N}`。
时间范围参数 `range`: `24h` | `7d` | `30d` | `custom`（配合 `from`、`to`，RFC3339）。

数据面限速：用户组与 API Key 各有每分钟请求数 / Token 数上限（0 不限），按最近 60 秒统计，请求数在准入时计数、Token 在结束时计数；超出返回 429 `rate_limited` / `token_rate_limited` 并带 `Retry-After`。Key 过期返回 401 `api_key_expired`，Key 白名单外的模型返回 403 `key_model_not_allowed`。

数据面接口（客户端调用，API Key 认证）：`GET /v1/models`、`GET /v1/models/{id}`、`POST /v1/chat/completions`、`POST /v1/responses`、`POST /v1/messages`、`POST /v1/messages/count_tokens`、`POST /v1/embeddings`、`POST /v1/images/generations`、`POST /v1/images/edits`、`POST /v1/images/variations`、`POST /v1/<自定义路径>`（自定义 JSON 接口账号声明的路径，见下）；Google Gemini 原生入口 `GET /v1beta/models`、`GET /v1beta/models/{model}`、`POST /v1beta/models/{model}:generateContent`、`POST /v1beta/models/{model}:streamGenerateContent?alt=sse`、`POST /v1beta/models/{model}:countTokens`（`/v1/models/{model}:generateContent` 等 `/v1` 前缀形式同样接受）。为兼容各种编程客户端：

- 认证头接受 `Authorization: Bearer`、`x-api-key`、`api-key`、`x-goog-api-key` 四种，以及 Gemini REST 风格的 `?key=` 查询参数；路径省略或重复 `/v1` 也接受；`/v1/*` 支持 CORS 预检，浏览器内的客户端可直连。
- **模型名解析**：客户端发来的模型名依次按精确、忽略大小写、去掉厂商前缀（`anthropic/`、`openai/`、`models/` 等）、去掉 `-latest`、版本 / 日期后缀容错（`claude-sonnet-4-5-20250929` 命中映射 `claude-sonnet-4-5`，反之亦可；只接受数字或日期形态的后缀，`gpt-5-codex` 不会命中 `gpt-5`）匹配映射；日志与报表记录解析后的名称。**歧义**（多个同长候选，或仅大小写不同的多个映射）返回 400 `model_ambiguous`，不会调用上游，也不会落入透传。
- **透传未映射模型**：账号开启 `passthrough_models` 后，没有映射的模型名原样转发给该账号（按账号类型限定文本 / 向量 / 图像）。显式映射优先；用户组绑定了模型组时不允许透传。
- **图片：文生图、图生图、变体同一账号**：图像类型的账号与它的模型映射同时承接 `POST /v1/images/generations`（文生图）、`POST /v1/images/edits`（图生图 / 局部重绘 / 多图合成）与 `POST /v1/images/variations`（变体），无需为图生图另建账号或映射。`edits` / `variations` 接受 `multipart/form-data`（OpenAI 形态：`image` 或 `image[]` 一张或多张、可选 `mask`、`prompt`、`model`、`n`、`size`、`quality` 等）与 JSON（上游以 URL / base64 传图时）；multipart 由网关解析后**原样重编码**转发到 `<服务地址>/images/edits` 等（每个部分的字段名、文件名、Content-Type、字节不变，只把 `model` 换成映射的上游名，`model` 缺失返回 400 `missing_model`，表单损坏返回 400 `invalid_body`），响应原样回传。请求体总大小受「性能设置 → 最大请求体」限制（默认 20 MB，多图编辑请按需调大，超限 413）。用量：响应 `usage` 里的 `input_tokens` / `output_tokens`（gpt-image 系列，缓存明细取 `input_tokens_details.cached_tokens`）或 `prompt_tokens` / `completion_tokens`（缓存明细取 `prompt_tokens_details.cached_tokens`）计入日志与计价，两套是别名、OpenAI 命名优先、不相加，缓存输入按缓存单价计，没有 `usage` 的图片模型（按张计费）用量为 unknown、成本未定价。
- **自定义 JSON 接口（非对话模型）**：类型为 `custom` 的账号声明自己提供的客户端路径（`endpoints`，写在 `/v1` 之后，如 `/systemone`）。客户端 `POST /v1/<路径>`，请求体必须是 JSON 且带 `model`；网关鉴权、解析模型（映射或透传，类型必须是 `custom`）、按用户组授权、限流、并发与账号健康筛选后，把请求体**原样**转发到 `<账号服务地址>/<路径>`（只改写 `model` 为映射的上游名，注入账号 Key 为 `Authorization: Bearer`，供应商 `auth_header` 为 `x-api-key` 时同时发该头），上游 JSON 响应**原样**回传（正文与 2xx 状态码都按上游的返回，日志记录同一状态码；附带 `X-Upstream-*` 头），不做任何协议转换，不支持流式。只有声明了该路径的账号才是候选，同一模型映射在多个账号上时按优先级 / 权重挑选声明了该路径的那些。用量取响应 `usage` 里的 `prompt_tokens` / `completion_tokens` 或 `input_tokens` / `output_tokens`：两套是别名、**绝不相加**，只要 OpenAI 命名的任一字段存在就用 OpenAI 那套，否则用另一套；存在与否按字段判断，显式的 0 是"已确认的零消耗"（用量状态 confirmed，成功与错误响应、重试链路里的每次尝试都按此计），`usage` 里四个字段都没有才是 unknown（错误响应无 `usage` 时仍按状态码推断）；`total_tokens` 只作参考，计数以分项为准。按模型名走价目表计价。路径规则：以 `/` 开头、无尾部 `/`、无查询串 / 锚点 / 空白 / `..`、≤128 字节、每账号最多 20 条，不能落在内置路由 `/chat`、`/responses`、`/messages`、`/embeddings`、`/images`、`/models` 之下；管理端保存时自动规范化：补前导 `/`，**按路径段**去掉开头的全部 `/v1` 段（`/v1/x`、`/v1/v1/x` 都存为 `/x`，因此端点自身首段不能是 `v1`；`/v1beta/x`、`/v10/x` 是不同的段，原样保留），去尾部 `/`，去重；规范化幂等，把读出的列表原样保存不会再改变路径，去前缀后落在内置路由上的输入首次保存即拒绝。数据面派发时再核对一次内置前缀，直接改库写入的 `/chat/...` 之类路径也不会被服务。未被任何启用账号声明的 `/v1/<路径>` 在通过 Key 认证后返回 404 `not_found`（未认证仍是 401，不泄露路径是否存在）；`custom` 类型模型出现在对话 / 向量 / 图像入口，或其他类型模型出现在自定义路径上，返回 400 `model_type_mismatch`。内置预设 `typesafe`（TypeSafe Jev，服务地址 `https://api.typesafe.ai/v1`，默认路径 `/systemone`）与 `custom-json`（任意 JSON 接口）。自定义接口账号保存与"测试"不做真实调用。
- `POST /v1/messages/count_tokens`：与生成请求走同一前半段（鉴权、正文上限与内存预算、模型解析、用户组与模型组授权、全局 / 组 / Key 并发槽），因此受同样的性能设置约束；有 Anthropic 协议账号可服务该模型时原样转发（改写模型名、透传 `anthropic-beta`），否则按请求体大小估算并带 `X-Token-Count-Estimated: true`。估算只用于上下文辅助，不是计量值，不进入用量账本。
- `GET /v1/models` / `GET /v1/models/{id}` 每项同时带 OpenAI 字段（`object:"model"/created/owned_by`）与 Anthropic ModelInfo 字段（`type:"model"/display_name/created_at`），列表含 `has_more`；网关自身分类放在 `model_type`（text / embedding / image），`kind` 为 model / virtual / group。透传账号可服务的未映射名称在详情查询中同样返回可用。
- 最终失败为 429 时把上游的 `Retry-After` 透传给客户端。
- **客户端识别**：每条日志记录 `client`（从 `User-Agent` 与 `x-app` / `originator` / `X-Title` / `x-client-name` / `Referer` / `HTTP-Referer` 识别的短标签；按整个产品名匹配而不是子串，`Referer` 只看主机名：`claude-code`、`codex`、`gemini-cli`、`opencode`、`cline`、`roo-code`、`cursor`、`kimi-code`、`zcode`、`hermes`、`deepseek-harness`、`anthropic-sdk`、`openai-sdk`、`genai-sdk`、`curl`、`python-http`、`node-http`、`browser`，其余取 User-Agent 第一个产品名，空为 `unknown`）与截断到 200 字符的 `user_agent`。管理端日志可按 `client` 筛选，`GET /api/admin/logs/filters` 返回出现过的 `clients[]`；用量报表（管理端与用户中心）附 `by_client`，它按明细日志统计（只覆盖日志保留期），不来自小时汇总，但使用与汇总相同的小时窗口 `[from 截到整点, to 所在小时末)`，各行之和与顶部汇总一致。
- **Gemini 原生协议**（`gemini-generate`）：模型名取自 URL，流式由 `:streamGenerateContent` 决定（总是以 SSE 回复，`usageMetadata` 随最后一个事件下发）；错误按 Google 的 `{"error":{"code","message","status"}}` 形状返回（`UNAUTHENTICATED` / `PERMISSION_DENIED` / `NOT_FOUND` / `RESOURCE_EXHAUSTED` …）；`countTokens` 与生成请求走同一鉴权 / 模型解析 / 授权检查后本地估算，不打上游。同协议直连到原生 Gemini 账号时请求体原样转发（Gemini 拒绝未知字段，因此不注入 `model`），只发送 `x-goog-api-key`；跨协议时 Gemini ↔ Chat 互转：`systemInstruction` ↔ system、`functionCall` / `functionResponse` ↔ tool_calls / tool（按函数名顺序配对合成调用 ID，连续的工具结果合并进一个 user 轮）、`inlineData` ↔ data URL 图片、`thought` 部分 ↔ reasoning、`thinkingBudget` ↔ `reasoning_effort`、`responseMimeType/responseSchema` ↔ `response_format`、`thoughtsTokenCount` 计入输出 token。

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
- `GET /api/admin/providers` → `[{key,name,base_url,types[],protocols[],account_types:[{key,name,base_url,protocols[]}],auth_header,discover,custom,icon,endpoints[]?,default_mappings:[{request_model,upstream_model}]?}]`。`endpoints` 与 `default_mappings` 只出现在没有模型列表接口的预设上（如 `typesafe`：`/systemone`、`jev → jev-latest`）：管理界面选中供应商时自动填入，创建账号时未给映射且未开透传则服务端按 `default_mappings` 填入，未给 `endpoints` 则按预设填入。`account_types[].protocols` 存在时表示该入口只讲这些协议（例如 DeepSeek / Kimi / 智谱 / MiniMax / 百炼 的 Anthropic 兼容入口只讲 `anthropic-messages`；Gemini 预设的"原生 Gemini API"入口只讲 `gemini-generate`，"OpenAI 兼容"入口讲 `openai-completions` / `openai-embeddings`），创建账号时协议默认与校验都以它为准，避免把不同协议混在同一个 Base URL 下。`POST /accounts/discover` 可带 `account_type` / `protocols`，原生 Gemini 入口按 `x-goog-api-key` 拉取 `models[].name` 并去掉 `models/` 前缀
- `GET /api/admin/models` → `[{name,type,kind:"model"|"virtual"|"group",provider,accounts,models[]}]`（当前可路由的全部请求模型）

### 账号池 `/api/admin/accounts`
账号对象：
```
{id, name, provider, account_type, type:"text"|"image"|"embedding"|"custom", endpoints:[], base_url, has_key:true, api_key_masked:"sk-ab…yz",
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
- `POST /api/admin/accounts/:id/cache-check` `{model}` → `{ok, hit, protocol, model, first:{prompt_tokens,cached_tokens,cache_write_tokens,latency_ms}, second:{...}, message}`：连续发两次约 1.5k Token 相同前缀的请求（Anthropic 协议带 `cache_control`），第二次的缓存字段非零即命中；用于证明提示缓存在该账号与模型上真实生效
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
- `GET`、`POST {name,max_concurrency,key_max_concurrency,token_quota,tokens_per_minute,requests_per_minute,model_group_ids[],enabled,note}`、`PUT /:id`、`DELETE /:id`（有成员 409 `has_members`；默认组 409 `is_default`）、`PATCH /:id/enabled`

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
- 对象：`{id,request_id,user_id,username,group_id,group_name,api_key_id,api_key_name,account_id,account_name,provider,request_model,upstream_model,model_group,api_type,client_protocol,upstream_protocol,stream,prompt_tokens,completion_tokens,total_tokens,cached_tokens,tokens_known,result:"success"|"client_error"|"upstream_error"|"blocked"|"rate_limited",status_code,latency_ms,upstream_latency_ms,first_byte_ms,first_content_ms,queue_wait_ms,client_write_ms,client,user_agent,error,attempts:[{account_id,account_name,provider,protocol,model,status_code,latency_ms,error}],route_label,client_ip,created_at}`
- `GET /:id`
- `GET /api/admin/logs/filters`（另返回 `clients[]`：日志里出现过的客户端标签） → `{users:[{id,username}], accounts:[{id,name,provider}], providers:[...], models:[...]}`
- 耗时字段口径（毫秒）。**时间点**（从请求进入网关起算）：`first_byte_ms` 收到**成功那次上游尝试的响应头**，包含排队与之前失败的尝试，不是客户端看到首个文字的时间；`first_content_ms` 首个带生成内容（非空正文、思考文本、带名字的工具调用开始事件或工具参数增量）的事件被连接接受的时刻，即完成该事件的那次 Write 成功返回之后；角色事件、空增量、签名、心跳、仅 usage 的事件不算；非流式为正文 Write 成功之后。**时长**：`latency_ms` 整个请求；`queue_wait_ms` 等待网关 / 用户组 / Key 并发槽的时段长度（不含账号并发槽），0 可能是没排队也可能是不足 1 ms；`upstream_latency_ms` 从发往上游到转发结束（流式时包含交错的客户端写入）；`client_write_ms` 流式时向客户端写入与刷新被阻塞的累计时间，偏大说明写入路径慢（下游接收、网络或本地写入），但反向代理可以先快速收完再自己攒，所以偏小不能排除反代缓冲。`first_content_ms − first_byte_ms` 是"上游响应头到网关首次写出内容"的间隔，包含上游等待、读事件、协议转换、调度和写入，非流式还包含读完整响应，不能单独归因给上游；`first_byte_ms − queue_wait_ms` 近似网关前置处理加上游首包。只有成功且确实写出过内容的样本才满足 `queue_wait_ms ≤ first_byte_ms ≤ first_content_ms ≤ latency_ms`；失败请求的 `first_byte_ms`、没有内容事件的流的 `first_content_ms` 以及升级前的历史行都是 0，比较前先排除。这些数与客户端侧测得的总耗时之间还隔着客户端连接与网络传输，不能直接相减归责。
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
- `PUT /basic {base_url, log_retention_days, protocol_conversion, reasoning_to_content, site_name}`。`reasoning_to_content` 开启后，跨协议转换成 OpenAI Chat 的响应把思考内容以 `<think>…</think>` 放进正文而不是 `reasoning_content`（流式与非流式），同协议直连不受影响
- `PUT /performance {max_concurrency, queue_size, queue_timeout_sec, request_timeout_sec, stream_idle_timeout_sec, max_body_kb, cooldown_sec, max_retries, upstream_connect_timeout_sec, max_body_memory_mb, vector_max_concurrency, vector_timeout_sec}`；所有字段不得为负（400），`max_retries / max_body_kb / max_body_memory_mb / vector_max_concurrency / vector_timeout_sec` 传 0 恢复默认值
- `PUT /vector {account_id, model}`；`POST /vector/test {account_id, model}` → `{ok, dim, latency_ms, message}`
- `PUT /smart_route {enabled, virtual_model, simple_group_id, complex_group_id, threshold, confidence_gap, top_k}`
- `PUT /compliance {enabled, semantic_threshold, check_system_prompt, on_failure:"allow"|"block"}`
- `PUT /elasticsearch {enabled,url,auth_type,api_key,username,password,index_prefix,request_kb,response_kb,retention_days}`；`POST /elasticsearch/test` → `{ok, version, message}`；`GET /elasticsearch/status` → `{configured, queue_count, queue_bytes, dropped, last_success_at, failing_since}`
- 密钥字段传 `"******"` 表示保持不变。

### 配置版本 `/api/admin/config/snapshots`
- 修改账号池、模型组、单价表、设置的任何写接口（探测类除外）执行前自动保存一份快照：账号（含加密密钥，以独立的持久化结构保存）与映射、模型组、用户组的模型组授权、完整单价表（空表也是一种状态）、全部设置节；载荷带 `version:2` 与 sha256 `checksum`；最多保留 50 份。
- `GET` → `{items:[{id,actor,reason,created_at}], total}`；`POST {reason}` 手动快照；`GET /:id` → 摘要（账号不含密钥，带 `version` 与 `corrupt`）；`POST /:id/restore` → `{restored, missing_keys[]}`：先校验 checksum（不符返回 409 `snapshot_corrupt`，不动任何数据），再在一个事务里用快照覆盖当前配置（先自动保存当前状态），随后重载设置、单价、网关快照、路由与合规引擎。1.0.12 早期写入的 v1 快照不含密钥：恢复时沿用同 id 账号当前的密钥；无处可取的账号以停用状态恢复、备注标记并列入 `missing_keys`。用户、用户组、Key、日志不在范围内。 价目表按整套恢复时，每行过与手工新增相同的校验并统一小写，同键多行合并为一行（改过的 > 手工 > 最早），响应另带 `price_rows_skipped[]`（无效行及原因）与 `price_rows_merged`（合并掉的行数），管理界面把两者显示为警告；恢复后会刷新价目、网关、向量、路由、合规全部运行态，任一失败不跳过其余，最后用一个 503 汇总：单个子系统失败时 `code` 为该子系统的码（`price_reload_failed` / `gateway_reload_failed` / `compliance_reload_failed` / `route_reload_failed`），多个时为 `runtime_reload_failed`；正文 `failed[]:{subsystem, code, error}`，并仍带 `restored` / `missing_keys` / `price_rows_*`。
- **运行态刷新的统一语义**：所有先写库再刷新内存副本的管理接口（账号、用户组、模型组、价格、策略组 / 敏感词 / 审计样本、路由样本、向量与合规设置、快照恢复）在刷新失败时返回上述 503，正文说明"数据已写入数据库，但运行态未刷新，请求仍按之前的配置处理"。数据库写入不回滚。503 正文带 `committed:true`；创建类接口还带已创建资源的 `id` 与 `resource`。**不要重复提交创建请求**：恢复动作是 `POST /api/admin/runtime/reload`（「设置 → 配置快照 → 重新加载运行态」，刷新价目、网关、路由、合规全部运行态，失败同样返回上述 503）或重启网关。即便重复提交，创建接口也是幂等的，且幂等键由**数据库唯一索引**强制（升级时在一个事务里（PostgreSQL 下加 advisory lock）自动整理旧数据并在同一事务里建好唯一索引（函数返回时键已生效，不存在"清理完、索引前"的写入窗口）：同名账号改名为 `<名> #<id>`（对照全部已用名称，不重名、UTF-8 边界截断），重复敏感词 / 样本合并为一行——保留带有效状态的那条：向量身份可被当前运行态加载的优先——身份按与运行时完全相同的规则解析（`vector.Identity`：一条 SQL 同时读账号 Base URL、类型、启停、密钥与映射后的上游模型，即 `id|url|upstream`；运行时的向量客户端也只从这一份快照构造，密钥、地址、模型必属同一账号代次）。Base URL 统一去掉首尾空白与尾部斜杠（`vector.NormalizeBaseURL`），客户端、运行态身份与迁移用同一结果。引擎存向量时打的身份是"产生该向量的快照"的身份（嵌入函数随结果一并返回；纯嵌入函数则用调用前读取的身份），配置在请求飞行期间切换不会把旧模型向量标成新身份；每次构建先用一条原子更新把本次构建的 claim 令牌（`build_token`）写到所选样本上，写回是条件更新（`id` + 读取时的 `text_hash` + 本次令牌）并核对影响行数——构建期间被编辑、删除，或已被更晚开始的构建重新领取（无论其身份是否相同、是否已写入）的样本计入 `failed` 且不写入。先后顺序由数据库对领取更新的原子性裁决，跨进程 / 多实例同样成立，可重复出现的模型身份不再参与判序。计数守恒：本次领取到的行数 = `built + failed`，对**每条退出路径**都成立——在领取与按令牌读回之间被更晚的构建抢走的行计入 `failed`（归更晚的构建所有）；上游报错或返回向量数不对时构建即停，当前批与其后已领取、未发送的批全部计入 `failed`（它们仍带本次令牌，下一次构建正常领取）；此前批次已写入的行仍计 `built`；绝不算作静默成功；按 ID 构建时读回也按主键限定，不扫全表；领取不改 `updated_at`，旧的空身份视为兼容；其次任一向量（同分时取最新的一份），再看启动；向量设置不是合法 JSON 或相关查询出错时迁移中止不删任何行。四个唯一索引按数据库目录**逐个按名核对**"确实是 UNIQUE、列正确、没有 WHERE 条件与表达式列、且每个键列用列自身的默认排序规则与升序"（SQLite 用 `PRAGMA index_list` / `index_xinfo` 并要求 `coll = BINARY`、`desc = 0`，PostgreSQL 查 `pg_index` 并限定所属表与当前 schema、要求 `indisvalid` / `indisready`、`indcollation` 等于列的 `attcollation`、`indclass` 为类型默认 operator class、`indoption = 0`，其它方言拒绝启动）——键按精确字符串比较，`NOCASE` / `RTRIM` / `DESC` 之类的同名索引不是它，同表上其它无关索引（表达式、部分、普通）不参与也不受影响，同名但定义不对的旧索引（普通、列错、部分、表达式）在同一事务里删掉重建；启用状态取"任一启用"，向量、非空备注、非零阈值从被删副本合并过来，每条被删行连同其向量身份记入日志）：账号按名称唯一（`uq_accounts_name`），再次创建时比较**全部生效字段**（供应商、账号类型、类型、地址、密钥、协议集合、映射集合、测试模型、优先级、权重、最大并发、透传、启停、备注），完全相同返回既有账号，任一不同返回 409 `account_exists` 并带既有 `id` / `resource`；敏感词按（策略组, 词）唯一，审计样本按（策略组, 文本 SHA-256）、路由样本按（标签, 文本 SHA-256）唯一（`text_hash` 列，摘要碰撞时核对原文），相同即返回既有行，备注 / 启停 / 阈值不同返回 409 `word_exists` / `sample_exists` 并带既有资源；重复样本带 `build_vector:true` 时仍对既有行执行向量构建并按新建同一合同报告（`build_error` / 索引重载 503）。实现是插入先行、由唯一索引裁决，撞键后才读取既有行比较；读取失败返回 500，不会再插入；并发相同创建只留一行。用户组、模型组、策略组本就名称唯一（409）；价格行按唯一键 409。批量导入路由样本（一次最多 2,000 条，超过 400）：批内重复计 `skipped`；键的预查与写入后的核对都按 200 个键一组做集合查询（300 条只有几次 SELECT）；已存在且备注完全相同（与单条创建同一比较）的样本计 `existing`，`build_vector:true` 时与新建行一起构建；备注不同的计 `conflicts` 不动；插入用 `ON CONFLICT DO NOTHING`，`created` 是数据库实际插入数，写入后按全部键核对，被别人抢先插入的行按同一规则归入 `existing`（并进入构建）或 `conflicts`；若写入已提交但核对查询失败，返回 503 `batch_reclassify_failed`（`committed:true`、`created`），不做构建，同一批次可直接重新提交（幂等，会补做核对与构建）。编辑账号名 / 敏感词撞键后读取冲突资源失败返回 500，不伪造 409。批量导入敏感词同样幂等（`ON CONFLICT DO NOTHING`），响应 `created` / `skipped`。编辑账号名或敏感词撞唯一键返回 409 `account_exists` / `word_exists` 并带冲突资源；重复键的识别基于驱动错误码（SQLite 2067 / PostgreSQL 23505，经 gorm `TranslateError`）。
- `POST /api/admin/runtime/reload` 本身不写库：失败返回 503 `runtime_reload_failed`，正文 `committed:false`、`failed[]`，提示可直接重试。
- **智能路由的配置代次**：网关快照保存整份 `SmartRoute` 设置（虚拟模型、启停、分组、阈值、规则参数），数据面与路由引擎只读请求所在快照的那一份；保存设置后快照重建失败（503）时，请求整体沿用旧代次，不会出现"旧快照 + 新分组"的混用，重建成功后整体切换。向量构建接口（路由样本 / 审计样本的新增、修改、批量构建）同样：向量已写库但索引重载失败返回 503（`route_reload_failed` / `compliance_reload_failed`，批量接口正文仍带 `built` / `failed`），向量生成本身失败仍是 200 加 `build_error` / `error`。
- `PUT /api/admin/settings/smart-route` 保存后刷新网关路由快照（整份智能路由设置在快照里），刷新失败返回 503 `gateway_reload_failed`，设置已入库但请求仍按旧快照的整份设置路由。
- **启动时的规则加载**：网关启动会对智能路由与合规引擎各做一次受检查的加载。合规已启用而规则读取失败时拒绝启动；合规未启用时记录警告。合规引擎在第一次成功加载之前处于"未加载"状态，此时若合规启用，所有请求按阻断处理（`DetectMethod:"unavailable"`，`Degraded` 带原因），绝不因规则读不到而放行。
- 创建价格 / 用户组 / 账号 / 策略组 / 敏感词 / 审计样本时带 `enabled:false`：插入与禁用在同一个事务里，第二步失败整体回滚，不会留下已启用的行。价格备注、导入备注按 UTF-8 字符边界截断（255 / 250 字节），不会产生非法 UTF-8。

### 计价 `/api/admin/prices`
- 对象：`{id, pattern, provider, input_per_m, output_per_m, cached_input_per_m, cache_write_per_m, currency:"USD"|"CNY", builtin, enabled, note, source, source_date, edited, updated_at}`（`source` 为空表示内置或手工行；`edited` 在管理员改过后置真），单价均为每百万 Token。`pattern` 精确匹配模型名或作为前缀匹配（分隔符 `-`、`:`、`@`），越长越优先；`provider` 为空表示任意供应商。匹配顺序：账号供应商专属行 → 任意供应商行 → 其他供应商的行（自定义中转站转发的 claude/gpt 模型也能计价）。
- `GET ?q=` → `{items, total, builtin_updated, currency}`；`POST`、`PUT /:id`、`DELETE /:id`；`POST /reset-builtin` 恢复内置参考价（自定义行保留）；`GET /lookup?provider=&model=` → `{found, price}`。
- 内置表随版本更新，启动时只补充缺失的内置行，不覆盖已编辑的行。
- 价目同步分**预览**与**应用**两步，应用只写入预览过的那份内容：
  - `POST /api/admin/prices/import {source:"litellm"|"easycpa"|"url", url?, overwrite_edited, overwrite_currency}`：下载并预览，不写库、不打配置快照。`source` 为 `litellm` 时下载 LiteLLM 的 `model_prices_and_context_window.json`，`easycpa` 时下载 EasyCLIProxyAPI 的 `model_prices.json`（`models` 为以模型名为键的对象，旧的数组形式也接受），`url` 时下载自定义地址（32 MB 上限，60 秒超时，走网关的出站 HTTP 客户端与代理设置）。响应 `{applied:false, plan_id, sha256, origin, options, expires_in, plan}`；`plan_id` 30 分钟内有效、用一次即失效，服务端最多保留 16 份 / 合计 50,000 行待应用的预览（单进程内存；多实例部署需会话亲和）。这两个上限是硬上限：单份目录超过 50,000 行返回 413 `import_too_large`；为新预览腾空间时只淘汰最旧的、未在应用中的预览，全部都在应用中时返回 429 `import_busy`；领取（apply）只清理过期项，不会因为缓存正好满而淘汰有效预览。带 `apply:true` 返回 400。
  - `POST /api/admin/prices/import-file`：multipart 上传同格式文件预览，字段 `file`、`overwrite_edited`、`overwrite_currency`。
  - `POST /api/admin/prices/import/apply {plan_id, sha256?}`：写入该预览解析出的目录（不再下载），应用前先打一份配置快照，事务内按当时的表重新规划，响应里的 `plan` 计数是实际写入的结果。`plan_id` 的领取是原子的：同一 `plan_id` 并发应用只有一个成功，其余 409；快照或写事务失败会释放该 `plan_id` 供重试，写入成功才作废。`plan_id` 过期 / 已用 / 正在应用 / `sha256` 不符返回 409。写库成功但运行态价目重载失败返回 503 `price_reload_failed`（数据库已是新价目，请求仍按旧价目计费，重试或重启网关）。
  - `plan`：`{source, date, total, new, updated, same, kept, skipped, invalid, invalid_rows?[], duplicates, changes:[{action:"new"|"update"|"keep", pattern, provider, old?:[in,out,cached,write], new:[...], old_currency?, new_currency, currency_changed?, reason?:"manual"|"currency"}]}`（`changes` 最多 500 条）。
  - 规则：每一行都过与手工新增相同的校验（名称 1–128 字符、供应商 ≤32、币种 USD/CNY、四个单价 0–1000000 且有限），不合格的计入 `invalid` 并列出原因，不写入；管理员手工新增（`builtin=false` 且 `source=""`）或手工改过（`edited=true`）的行默认保留（`reason:"manual"`），`overwrite_edited=true` 才覆盖；现有行币种与来源不同（典型：国内供应商的人民币内置行对上 LiteLLM 的美元国际价）默认保留（`reason:"currency"`，变化行带新旧币种），`overwrite_currency=true` 才改；其余内置行与之前导入的行原地更新（`builtin` 标记保留，「恢复内置」仍能还原官网参考价）；现表中同一（供应商, 模型）有多行时应用会合并为一行（改过的优先，其次手工行，再取最早的），预览里 `duplicates` 给出数量。LiteLLM 只导入能映射到本网关供应商的条目（openai / anthropic / gemini / xai / deepseek / moonshot / dashscope→aliyun / volcengine / minimax / zhipu / mistral / groq / together / fireworks / cerebras），转售商（bedrock / azure / openrouter 等）跳过，非文本模式跳过，模型名去掉供应商前缀；EasyCLIProxyAPI 的行没有供应商信息，作为通用行导入。导入行 `source` / `source_date` 记录来源，来源单价一律美元。
- 价格行的键 `(provider, pattern)` **唯一**且统一小写存储（唯一索引 `uq_model_prices_key`，升级时先把旧行小写并按上面的优先级去重）；新增 / 修改撞键返回 409 `price_exists`。价格 CRUD 与「恢复内置」在写库后重载运行态失败同样返回 503 `price_reload_failed`。
- `PUT /api/admin/settings/pricing {currency:"CNY"|"USD", usd_to_cny}`：**显示币种**与汇率。账本固定为美元：人民币单价行按汇率折成美元入账，展示时再按显示币种换算；切换显示币种或汇率只改变展示，历史金额随之整体换算，不会被换标签或混币种累加。`GET /api/public/info`、价目表与用量报表都返回当前 `currency`，价目表另带 `ledger:"USD"`。
- 费用在每次尝试结束时按当时单价估算并冻结：`call_logs.cost_micros`（美元账本的百万分之一单位，`cost_ledger:"USD"` 标记该行已按账本记账；无标记的旧行由启动迁移与 journal 回放按库来源逐行判定并各处理一次，判定不了的标为 `"unverified"`：原始数值保留在 `cost_micros`，`cost_known=false`，日志的 `cost` 为 0 且 `cost_unverified=true`，小时汇总不计其金额、只计入 `cost_unverified` 条数，用量报表 `summary.cost_unverified` 与各分布项 `cost_unverified` 给出条数；对账结果 `Mismatch` 增加 `log_cost / rollup_cost / log_unverified / rollup_unverified`）、`cost_known`（任一有 Token 的尝试无单价、或任一尝试用量为 unknown / partial，则为 false：此时金额只是已知部分的下限）；小时聚合 `cost_micros` 按尝试账号归属。管理端日志列表 / 详情与用户日志都带按显示币种换算后的 `cost`；用量报表 `summary.cost`、各分布项 `cost`、趋势点 `cost` 以及 `currency`；概览 `cost.total`。输入 Token 分三段计价：缓存读（`cached_tokens`，缓存价）、缓存写（`cache_write_tokens`，来自 Anthropic `cache_creation_input_tokens`，按 `cache_write_per_m`，该行没填则按输入价）、其余按输入价。单价修改后不回溯历史记录。

### 备份与还原 `/api/admin/backups`
- 备份包是 tar.gz：`manifest.json`（`format:1`、`app_version`、`created_at`、`db_driver:"sqlite"`、`files` 每个文件的 SHA-256）加 `data/db/yzapi.db`、`data/journal/*`、`data/security/credential.key`（上游 Key 的加密密钥）与 `jwt.key`。一致性：先把 journal 与密钥复制成私有副本（边复制边算校验和），再用 `VACUUM INTO` 对运行中的数据库取快照，归档只写这些副本；检查点在包内固定写 `0`，还原后整份 journal 重放，已在快照里的记录按 `request_id` 幂等跳过，因此备份开始前已落盘的计量恰好恢复一份，打包期间的追加、提交、检查点推进与轮转都不影响已归档的字节。**只支持默认路径的内嵌 SQLite**（数据目录下 `data/db/yzapi.db`）：PostgreSQL 或通过 `YZAPI_DB_DSN` 指定了自定义 SQLite 路径时，列表返回 `supported:false` 与 `unsupported_reason`，创建、还原、开启自动备份与命令行还原都返回 400 `backup_unsupported`；请用 `pg_dump` 或自行备份该数据库文件，并单独保存 `data/security`。备份包含全部上游 Key 的解密材料，下载与保管按密钥对待。
- `GET` → `{items:[{name,size,created_at}], pending_restore, db_driver, supported}`（数据目录 `backups/` 下的本地备份，新到旧）；`POST` 立即生成一份 → `{name,size,created_at}`，文件名 `yzapi-backup-<YYYYMMDD-HHMMSS>-<版本>.tar.gz`；`GET /:name/download` 下载（`application/gzip`）；`DELETE /:name`。文件名不合法 400，不存在 404。
- `POST /restore`（multipart 字段 `file`，上传上限 2 GB、解包总量上限 16 GB）：每次上传先解包到自己的私有目录 `data/restore-incoming-*` 并校验（必须有 manifest、格式版本一致、路径只允许 `data/db/yzapi.db`、`data/journal/*`、`data/security/*`、manifest 列出的每个文件都在且校验和一致、包里没有 manifest 未列出的文件、必须含数据库与 `credential.key`、数据库能只读打开且含 `users` 表并通过 `integrity_check`），任一不满足返回 400 `backup_invalid` 且**不改动任何现有文件**，也不影响已经在等待的还原；校验通过后以一次改名发布为 `data/restore-staging/`，同一时间只允许一份等待中的还原，已有一份时返回 409 `restore_pending`（并发上传只有一份成功，内容不会混合）。校验通过返回 `{staged:true, restart:"scheduled", backup:manifest}`，约 1.5 秒后进程向自己发 SIGTERM 优雅退出，由容器重启策略 / systemd `Restart=always` 拉起；下次启动在打开数据库之前把暂存文件换入，被替换的 `data/db`、`data/journal`、`data/security` 整体移到 `data/pre-restore-<时间>/` 保留。换入可在任意一步中断后续做：对每个目录，暂存里还在就说明未装入（先把现有的移到保留目录再装入），暂存里没有了就说明已装入（不再动它），保留目录名记在暂存区里续用；最后确认数据库与 `credential.key` 都在位才删除等待标记。确认失败时网关拒绝启动并在日志中指出暂存与保留目录，不会在半途状态上初始化空库。还原后的登录密码是备份时的密码。
- 命令行：`yzapi -restore <备份包>` 在停止的实例或新服务器上直接还原并退出（同样的校验与换入），之后正常启动。
- `PUT /api/admin/settings/backup {enabled, hour_local:0-23, keep_count:0-365}`：每日自动备份策略，按服务器本地时间在 `hour_local` 整点生成一份到 `backups/`（该小时内已有备份则跳过，重启不会重复生成），随后只保留最新 `keep_count` 份（0 为全部保留）；PostgreSQL 实例开启返回 400 `backup_unsupported`。`GET /api/admin/settings` 的 `backup` 节返回当前策略。

### 系统
- `GET /api/admin/system/info` → `{version, go_version, db_driver, uptime_sec, started_at, data_dir}`

## 用户中心 `/api/user/*`（任意登录用户）
- `GET /api/user/models` → `{base_url, models:[{name,type,kind,provider}]}`（仅本人用户组可用的模型）；`custom` 类型的模型另带 `endpoints:[]`（可调用它的 `/v1/<路径>` 列表，取自映射了该模型的启用账号的并集，用户中心接入指引据此生成示例）。
- `GET /api/user/keys` → `[{id,name,prefix,suffix,masked:"sk-abcd…wxyz",enabled,last_used_at,created_at}]`
- `POST /api/user/keys {name, expires_at?, allowed_models?[], tokens_per_minute?, requests_per_minute?}` → `{key:"sk-完整明文（仅此一次）", item:{...}}`。`expires_at` 为 null 表示永不过期；`allowed_models` 只能是本人可见的模型或模型组名，空表示继承用户组；每分钟上限 0 表示不限制，按最近 60 秒统计。Key 对象另含 `expires_at, expired, allowed_models, tokens_per_minute, requests_per_minute`
- `PUT /api/user/keys/:id` 同 POST 字段；`PATCH /api/user/keys/:id/enabled {enabled}`；`DELETE /api/user/keys/:id`
- `GET /api/user/usage?api_key_id=&model=&api_type=&range=&from=&to=&group_by=` → 同管理端用量结构（仅含 by_model / by_api_key / by_provider）
- `GET /api/user/logs?api_key_id=&model=&result=&range=&q=&page=` → 同调用日志对象（脱敏，不含账号信息）
- `GET /api/user/group` → `{id,name,max_concurrency,key_max_concurrency,token_quota,tokens_used_month,model_groups:[{id,name,models[]}]}`

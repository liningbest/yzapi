# 1.0.3 / 1.0.4 变更说明与验收要点

基线：1.0.2 = `9f80afa`。发布包为桌面 `yzapi-release.tar.gz`（`VERSION` 文件标明版本），镜像标签 `yzapi/gateway:<版本>`。

## 1.0.3（`238f713`、`e4495ba`）：后台管理复核 A01–A08 修复

对应 `docs/admin-review-9f80afa-2026-09-09.md`，评审方 7 个独立用例已收入仓库 `internal/api/review_admin_test.go`。

| 编号 | 问题 | 修改 | 验收方式 |
|---|---|---|---|
| A01 P1 | 退出后旧查询回填缓存，作废 token 再次获准 | 后台用户缓存加失效代次：回源前记录、回填前比对，失效期间开始的查询不写回；退出 / 禁用 / 改密共用 | `TestAuditLogoutCacheRefill`；手工：退出后用旧 token 调 `/api/auth/me` 必须 401 |
| A02 P1 | 两个管理员并发互禁，最终无管理员 | 禁用 / 降级 / 删除管理员在 `adminMu` + 事务内重新计数再写入 | `TestAuditConcurrentLastAdmin`：恰好一个 409，至少保留一名管理员 |
| A03 P1 | 并发登录失败丢计数，不锁定 | 失败计数改数据库原子递增，锁定基于递增后的值 | `TestAuditConcurrentFailedLogins`：3 次历史 + 2 次并发 → 锁定 |
| A04 P1 | 改账号地址后旧冷却仍阻断 | 更新前快照"地址是否变化"，事务成功后重置健康 | `TestAuditBaseURLChangeResetsCooldown`：只改地址、Key 不变，下一次请求直达新上游 |
| A05 P1 | 模型组删除部分提交 | 新增 `settings.Store.WithTx` / `SaveIn`：分组、关联表、智能路由引用同一事务并持设置写锁 | `TestAuditDeleteModelGroupSettingFailure`：设置写入失败时分组与引用全部保持原状 |
| A06 P2 | 编辑路由样本不重建向量 | 更新前快照"文本是否变化"；构建失败经 `build_error` 返回 | `TestAuditRouteSampleEditBuild` |
| A07 P2 | 空向量配置被"模型必填"阻止 | 模型必填依赖是否选账号；账号为空时选择框显示占位符而非 "0" | 浏览器：向量账号未配置、模型为空时点保存成功 |
| A08 P2 | 三个性能参数允许负数 | `max_body_memory_mb`、`vector_max_concurrency`、`vector_timeout_sec` 非负校验 | `TestAuditNegativeNewPerformanceFields`：传 -1 返回 400 |

接口变化：`PUT /api/admin/route/samples/:id` 响应可含 `build_error`；`PUT /api/admin/settings/performance` 全字段拒绝负数。

## 1.0.4（`4ce187c`）：多客户端兼容层

目标：Codex、OpenCode、Claude Code、Cline、DeepSeek / MiniMax 等客户端不做逐个适配即可接入。规则和转换边界见 `docs/api.md` 数据面一节。

### 行为变化

| 变化 | 说明 | 验收方式 |
|---|---|---|
| 模型名容错解析 | 依次：精确 → 忽略大小写 → 去厂商前缀（`anthropic/`、`openai/`、`models/` 等）→ 去 `-latest` → 版本 / 日期后缀（`claude-sonnet-4-5-20250929` ↔ `claude-sonnet-4-5`，仅数字或日期形态，`gpt-5-codex` 不命中 `gpt-5`，同长多候选视为歧义）。日志与报表记录解析后的名称 | `TestCompatModelNameResolution`；手工：用 `Claude-Sonnet-4-5`、`anthropic/claude-sonnet-4-5` 调用均命中同一映射；`claude` 单词 404 |
| 账号"透传未映射模型" | 账号新增 `passthrough_models`（UI 开关）。开启后无映射的模型名原样转发到该账号，按账号类型限定；显式映射优先；用户组绑定模型组时不允许透传；此类账号允许没有映射，保存时无测试模型则跳过连接验证 | `TestCompatPassthroughAccount`；api-crud 兼容项 |
| `POST /v1/messages/count_tokens` | 有 Anthropic 协议账号可服务该模型时原样转发（改模型名、透传 `anthropic-beta`），否则按请求体估算并带 `X-Token-Count-Estimated: true` | `TestCompatCountTokens` |
| `GET /v1/models/{id}`；模型列表字段 | 列表每项同时含 OpenAI（`object/created/owned_by`）与 Anthropic（`display_name/created_at`）字段，含 `has_more` | `TestCompatModelsCorsApiKey` |
| CORS、`api-key` 头 | `/v1/*` 支持预检；认证头新增 `api-key` | 同上；api-crud 兼容项 |
| `Retry-After` 透传 | 最终 429 时携带上游的 `Retry-After` | `TestCompatRetryAfterPropagated` |
| Responses 有状态字段 | `previous_response_id` 且上游非原生 Responses 时返回 400 并说明，不再静默丢弃 | `TestCompatCodexConversionLimits` |

### 客户端样本（`internal/gateway/compat_test.go`）

- Claude Code：Messages 请求含 `cache_control` 系统块、thinking + `signature`、`tool_use` / `tool_result`、`metadata`、`thinking` 参数、未知扩展字段、`anthropic-beta` 头；同协议直连时上游收到的正文除模型名外逐字段相同，头部与路径正确。
- Codex：Responses 请求含 `instructions`、`function_call` / `function_call_output`、`reasoning`、`store:false`、`prompt_cache_key`、`include`、`text.verbosity`；原生 Responses 上游逐字段相同；转到 Chat 上游时无状态请求可转换，有状态请求 400。
- OpenCode / Cline：Chat 工具调用往返转到 Anthropic 上游，`tool_use` / `tool_result` 与调用 ID 保留，厂商前缀模型名被解析。
- Anthropic 流式透传：`ping` 事件、注释行、300 KB 单行工具参数不截断、不乱序，用量正确入账。

### 已知边界（写入 `docs/api.md`）

跨协议转换会丢失 `cache_control`、供应商 thinking 签名、`metadata` / `service_tier` / `prompt_cache_key` 等扩展字段。建议每个供应商账号开启原生协议让客户端走同协议路径，转换只做兜底。

## 两个版本共同的验证结果

`go test -race -count=1 ./...` 通过；`bash scripts/smoke.sh` 47 项；`python3 scripts/api-crud.py` 84 项；`pnpm typecheck && pnpm build` 通过；评审方 7 个 `TestAudit*` 用例通过。

## 升级

镜像标签改为 `1.0.4` 重建即可。数据库自动迁移新增 `accounts.passthrough_models` 列（默认关闭），无需人工操作。

## 1.0.5：复核 `docs/release-review-1.0.3-1.0.4-2026-09-09.md` 的 G01–G07

| 编号 | 修改 | 验收 |
|---|---|---|
| G01 P1 | 缓存条目记录读取时的失效代次，命中时校验；退出后即使旧查询写回也不会被采信。保留代次检查，增加测试钩子 `authBeforeStoreHook` | `TestReview104LogoutAfterGenerationCheck`（钩子在比较通过与写回之间插入退出） |
| G02 P1 | `guardedAdminWrite` 内重新读取目标用户，按当前角色 / 启用状态判断；禁用、降级、删除共用；会话版本以数据库当前值递增 | `TestReview104AdminRoleSnapshot`（提升 B、降级 A 交错于禁用 B 之间，最终仍有管理员） |
| G03 P1 | `count_tokens` 改走生成请求同一前半段：正文上限（413）、内存预算、模型解析、授权、全局 / 组 / Key 并发槽，失败不调上游、资源归还 | `TestReview104CountTokensLimits`（1 KB 上限 → 413；全局并发占满 → 不到上游） |
| G04 P2 | 授权规则与生成路径共用，模型组名称在展开前即获准 | `TestReview104CountTokensAuthorizedGroup` |
| G05 P2 | 模型项 `type` 固定为 `"model"`（Anthropic ModelInfo 契约），网关分类移至 `model_type` | `TestReview104ModelMetadataType` |
| G06 P2 | 无类型的详情查询按 text / embedding / image 顺序查找透传账号 | `TestReview104PassthroughModelLookup` |
| G07 P2 | 解析结果区分"未匹配"与"歧义"：歧义（同长多候选、仅大小写不同的多映射）返回 400 `model_ambiguous`，不进透传、不调上游 | `TestReview104AmbiguousNameWithPassthrough` |

评审方 7 个用例已收入仓库（`internal/api/review104_test.go`、`internal/gateway/review104_test.go`），并用评审方自己的 overlay 脚本复跑通过。第 3 节的口径说明已采纳：请求样本测试不等同于真实客户端接入验收；SSE 注释行由网关消费不透传，仅事件透传；`count_tokens` 估算不进入用量账本。

## 1.0.6：复核 `docs/release-review-1.0.5-2026-09-09.md` 的 H01、H02

| 编号 | 修改 | 验收 |
|---|---|---|
| H01 P1 | `count_tokens` 选定上游后领取与生成请求相同的账号并发槽（`max_concurrency`），已满账号跳过、不发请求；正常、失败、换账号都释放 | `TestReview105CountTokensAccountLimit`（账号上限 1 且已占满 → 上游收到 0 个请求） |
| H02 P2 | 版本 / 日期后缀匹配阶段把代表多个大小写变体的候选直接视为歧义，返回 400 `model_ambiguous`，不调上游、不进透传 | `TestReview105CaseCollisionWithVersionSuffix` |

评审方 3 个 `TestReview105*` 用例收入仓库（`internal/gateway/review105_test.go`），含取消后资源归还与计数不进用量账本的通过项。

## 1.0.7：复核 `docs/release-review-1.0.6-2026-09-09.md` 的 H02 遗留

| 表现 | 修改 | 验收 |
|---|---|---|
| `demo`、`demo-1`、`DEMO-1` 下请求 `Demo-1-20260101` 选中了 `demo` | 后缀匹配先按最长候选决定；最长候选若代表多个大小写变体则歧义，不再回落到更短候选 | `TestReview106ResolutionComposition/duplicate_longer_candidate` |
| `demo`、`DEMO` 开透传时请求 `demo-coder` 被误判歧义 | 只有多出的段像版本 / 日期时才参与歧义判断；`coder` 不是版本，视为未匹配，按透传转发原名 | `TestReview106ResolutionComposition/non_version_variant_passthrough` |

评审方 `TestReview106*` 三个用例（含账号槽位在 200 / 404 / 500 后释放、取消释放）收入仓库 `internal/gateway/review106_test.go`。

## 1.0.9：流式转发排查

| 修改 | 说明 |
|---|---|
| 流式上游请求声明 `Accept-Encoding: identity` | 上游或其前置代理若对事件流做 gzip，token 会被攒成块延迟到达；非流式请求不受影响 |
| `YZAPI_UPSTREAM_HTTP2=0` | 强制对上游只用 HTTP/1.1，用于排查个别供应商 HTTP/2 流式输出不畅；默认仍允许 HTTP/2 |

回归：`TestCompatStreamRequestsRefuseCompression`。

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

## 1.0.9：流式转发排查手段（未证实为根因）

| 修改 | 说明 |
|---|---|
| 流式上游请求声明 `Accept-Encoding: identity` | 事件流压缩无收益，压缩端不及时刷新时会延迟送达；这是合理默认，但**尚未证实**是本次 tok/s 差距的原因 |
| `YZAPI_UPSTREAM_HTTP2=0` | 排查用开关，强制对上游只用 HTTP/1.1；默认仍允许 HTTP/2，不因单次测试结果固化 |

回归：`TestCompatStreamRequestsRefuseCompression`。与 1Panel 网关的 tok/s 差距根因未定，正确的对比方法见 1.0.10。

## 1.0.10：把"时间花在哪一段"变成可测

| 修改 | 说明 |
|---|---|
| 调用日志新增 `client_write_ms` | 流式转发时向客户端写入与刷新被阻塞的时间；`upstream_latency_ms − client_write_ms` 约等于纯上游等待。此前 `upstream_latency_ms` 在转发结束后记录，包含交错的客户端写入，不能当作模型生成耗时 |
| `scripts/stream-timeline.py` | 对任一 Base URL 发一条流式请求，打印响应头、首 token、总耗时、chunk 间隔分位数和"成批到达"比例。配合固定节奏的 mock 上游（`tools/mockupstream -delay 50ms` 作为账号），分别打服务端口与域名反代，可以直接看出延迟出现在哪一段 |

对比方法：固定账号、协议、请求参数与 `max_tokens`，新旧版本或不同开关交替执行、每组不少于 10 次，同时比较首字、完成时间、出字间隔与输出量；单看 tok/s 会被首字时刻的变化误导。

## 1.0.11：供应商预设扩充与 Anthropic 兼容入口

| 修改 | 说明 |
|---|---|
| 账号类型可带独立协议集 | DeepSeek、Kimi、智谱、MiniMax、阿里云百炼各新增"Anthropic 兼容（Claude Code）"账号类型，Base URL 指向各家的 `/anthropic` 入口、只讲 `anthropic-messages`；此前这些预设把 OpenAI 与 Anthropic 协议混在同一个 `/v1` 下，Claude Code 同协议直连会打到不存在的 `/v1/messages` |
| 创建 / 编辑账号按账号类型校验协议 | 选择 Anthropic 兼容入口时协议默认且只能为 `anthropic-messages`，前端切换账号类型自动刷新协议与地址 |
| 新增 9 个预设 | 阶跃星辰、百度千帆、Groq、Mistral、Together AI、Fireworks AI、Cerebras、LM Studio、自定义 (Anthropic 兼容)，共 26 家 |

回归：`TestRegistryConsistency`（预设一致性、Anthropic 入口不得复用 `/v1`）、`TestAccountTypeNarrowsProtocols`。

## 1.0.12：同类工具考察后的十项补充

对应 `docs/competitive-survey-2026-09.md` 第 3 节的 1–10 项。没有引入配额预占，也没有内置订阅账号 OAuth 池。

| 项 | 修改 | 说明 | 验收方式 |
|---|---|---|---|
| 1 成本估算 | 内置参考价目表（按模型、USD / CNY、每百万 token，标注 `2026-06` 版本），首次启动写入、可在"设置 → 计价"逐条改价或一键恢复内置；网关按每次尝试计价（缓存 token 按答复尝试计），折算成本记入日志、小时汇总、报表、总览与用户中心；`settings.pricing` 设基准币种与汇率 | 价格查找顺序：同供应商 → 通用 → 其他供应商；精确 > 最长前缀，容忍厂商前缀与版本后缀。任一尝试无价时 `cost_known=false` | `internal/pricing` 单测、`TestCostPerAttemptAndRollup` / `TestCostUnknownWhenUnpriced`、api-crud 计价用例；后台改价后新日志的 `cost` 立即变化 |
| 2 缓存自检 | 账号"映射"弹层的"缓存自检"按钮：用同一段 ~1.5k token 前缀连打两次，展示两次的 prompt / cached / cache_write 与延迟，判定第二次是否命中 | 支持 Anthropic（`cache_control`）、OpenAI Chat / Responses（自动前缀缓存）、Gemini（隐式缓存 `cachedContentTokenCount`） | `POST /api/admin/accounts/{id}/cache-check`；对真实供应商第二次 `cached>0` |
| 3 Key 级限制 | API Key 可设有效期、模型白名单（只能是自己可见的模型）、每分钟请求 / token 上限；用户组也可设每分钟请求 / token 上限 | 数据面：过期 401 `api_key_expired`，白名单外 403 `key_model_not_allowed`，超限 429 `rate_limited` / `token_rate_limited` 带 `Retry-After: 5`；按最近 60 秒滚动统计，请求在准入计数、token 在结束计数 | `TestKeyRestrictionsAndGroupLimits`、api-crud 用例；用户中心新建 / 编辑 Key 的表单 |
| 4 Gemini 原生协议 | 新协议 `gemini-generate`：客户端入口 `/v1beta/models/{model}:generateContent`、`:streamGenerateContent?alt=sse`、`:countTokens`、`GET /v1beta/models[/{model}]`，认证支持 `x-goog-api-key` 与 `?key=`；错误按 Google 形状返回。上游侧 Gemini 预设拆成"原生 Gemini API（Gemini CLI）"与"OpenAI 兼容"两种账号类型，自定义 / New API 预设也可勾选该协议；账号探测、发现模型、缓存自检都支持原生入口 | 四种客户端协议与四种上游协议两两互转：Gemini ↔ Chat 直接转换，Gemini ↔ Anthropic / Responses 经 Chat 分片串联；工具调用、图片、思考部分、`thinkingBudget`、JSON 输出模式都有映射；同协议直连时请求体原样转发、只发 `x-goog-api-key`（Google 会校验多余的 Bearer 头） | `internal/gateway/convert/gemini_test.go`（5 个）、`internal/gateway/gemini_test.go`（5 个端到端：Gemini CLI 形态直连、Gemini 客户端 → OpenAI 上游、OpenAI 客户端 → Gemini 上游、Claude Code → Gemini 上游流式、模型列表与错误形状）、smoke 新增 9 项（56）、mock 上游新增 Gemini 端点 |
| 5 思考转正文 | "设置 → 基础"开关：跨协议转换时把 thinking / reasoning 以 `<think>…</think>` 放进正文，供不认识 `reasoning_content` 的客户端显示；`thinking.budget_tokens` ↔ `reasoning_effort` 双向映射 | 只影响转换路径，同协议直连不动 | `TestReasoningToContentMode`；开关打开后 Chat 客户端调 Anthropic 上游能在正文看到思考 |
| 6 解析可见性 | 每个响应带 `X-Upstream-Account`、`X-Upstream-Model`、`X-Upstream-Protocol` | 便于在客户端侧核对"打到了哪个账号 / 模型 / 协议" | `TestCompatUpstreamHeaders`；curl -i 查看 |
| 7 熔断探测 | 账号冷却到期后只放一个探测请求（半开），成功即恢复、失败则重新冷却；探测超时 30 秒自动释放 | 避免冷却结束后一批请求同时撞上仍在故障的上游 | `TestHealthHalfOpenProbe`；smoke 的 failover 段 |
| 8 组级限速 | 见第 3 项：用户组每分钟请求 / token 上限 | | 同上 |
| 9 配置版本 | 账号、模型组、价目、设置的每次改动前自动快照；"设置 → 配置版本"可查看、手工打点、一键回滚（事务内整体恢复，快照里没有的设置段回到默认） | `GET/POST /api/admin/config/snapshots`、`POST /{id}/restore` | `TestConfigSnapshotAndRestore`、api-crud 用例 |
| 10 加权灰度 | 账号增加权重：同一优先级内按权重随机排序，优先级仍是硬序 | 权重 0 视为 1；用于新供应商小流量灰度 | `TestOrderUpstreamsWeighted`（分布检验） |

验证：`go test -race ./...` 全绿；`scripts/smoke.sh` 56 项；`scripts/api-crud.py` 90 项。登录页与 README 已把 Gemini CLI 列入适配客户端。

同版本附带一个修正：此前 `HTTP_PROXY` / `HTTPS_PROXY` 被当作显式代理地址读入，`NO_PROXY` 不生效，连回环地址的上游也会绕代理（本机压测因此每请求多 4 ms、封顶约 250 req/s）。现在只有 `YZAPI_HTTP_PROXY` 是强制代理，其余情况遵循标准环境变量语义。性能对比（零延迟 mock，清掉代理后）：1.0.12 相对十项之前单请求多约 10 µs，16 并发吞吐 1.4–1.7 万 req/s 对 2 万 req/s，只在机器打满时可见。

用户中心（`56a3d28`）：用量统计新增估算费用统计卡与分项费用列，调用日志新增费用列与详情项，未计价显示虚线短横并有提示。

验收清单：`docs/acceptance-1.0.11-1.0.12.md`。

升级：直接替换镜像；首次启动写入内置价目表与 `usage_hourlies.cost_micros` 等新列，不重算历史成本（历史日志 `cost_known=false`）。

## 1.0.13：独立验收复核 R112-01…05（`docs/acceptance-review-1.0.12-2026-09-11.md`）

| 编号 | 问题 | 修改 | 验收方式 |
|---|---|---|---|
| R112-01 P1 | 配置回滚清空账号密钥（`Account.APIKeyEnc` 是 `json:"-"`，快照根本没存） | 快照改用独立持久化结构 `snapshotAccount`（公共 Account 不变，浏览器摘要仍不含密钥）；载荷 `version:2` + sha256 校验，校验不符 409 且不动数据；v1 旧快照恢复时沿用同 id 账号当前密钥，无处可取的账号停用恢复并在 `missing_keys` / 界面提示中列出 | `TestR112RestoreKeepsCredential`（含网关看到解密后的 key）、`TestR112RestoreV1SnapshotWithoutKeys`、`TestR112RestoreRejectsCorruptSnapshot`；api-crud 恢复后真实探测 |
| R112-02 P1 | 切换币种给历史金额换标签、混币种累加 | 账本固定美元：计价时把人民币单价折成美元入账，展示时按显示币种换算；`cost_micros` 语义改为美元微单位，所有出口经 `Display` 换算；管理端日志新增换算后的 `cost`；升级时一次性把早期构建的金额折成美元（`cost_ledger` 标记） | `TestR112CurrencyDoesNotRelabelHistory`（1 USD → 7.2 CNY，新旧行同口径求和）、`TestCostAndCurrency`；api-crud 币种切换用例 |
| R112-03 P1 | 缓存写入单价未参与计算 | `convert.Usage` 新增内部字段 `CacheWriteTokens`，Anthropic 非流式 / 流式 / 直连 / 转换 / 串联流全部保留；尝试记录与日志新增 `cache_write_tokens`；`Cost` 增加 cacheWrite 参数，按 `cache_write_per_m` 计价（未填按输入价），读 / 写 / 普通输入互不重叠 | `TestCacheWritePricing`、`TestR112CacheWritePricing`（五条路径均为 3750 微美元） |
| R112-04 P2 | 未知用量尝试被忽略仍标"费用已知" | 任一尝试 usage 为 unknown / partial 即 `cost_known=false`，金额保留为已知部分 | `TestR112UnknownAttemptCost` |
| R112-05 P2 | 空价目表不能正确回滚 | 快照 `prices` 改为指针：字段存在即整体恢复（空集合也恢复为空），只有 v1 快照缺字段才不动 | `TestR112RestoreEmptyPrices`；api-crud 空价目表恢复用例 |

评审方 `docs/review-repros/accept112/run.py` 六个断言全部通过（`TestReview112SnapshotContainsEncryptedKey` 的夹具因持久化结构改名改为经 `captureConfig` 断言）。全量 race、smoke 56 项、api-crud 90 项通过。「设置 → 计价」的文案改为"显示币种"，并说明账本固定为美元。

## 1.0.14：第二轮复核 R113-01…03 与并行评审意见（`docs/acceptance-review-1.0.13-2026-09-11.md`）

| 编号 | 问题 | 修改 | 验收方式 |
|---|---|---|---|
| R113-01 P1 | 账本迁移遗漏尝试记录内的金额，重建聚合回到旧币种数值 | 迁移逐行处理 `call_logs`：请求金额与 `attempts[].cost_micros` 一起折算并打 `cost_ledger="USD"`；标记升为 `usd-v2`，已跑过 1.0.13 第一版迁移（标记 `usd`）的库只补转尝试金额 | `TestR113MigrationRollupConsistency`（迁移后 `logstore.Aggregate` 与请求金额一致）、`TestLedgerV1Upgrade` |
| R113-02 P1 | 迁移与标记不在同一事务，失败重试重复折算 | 整个迁移在一个事务内：先占位标记（并发实例只有一个成功）、再改数据、最后写版本；读标记只把 RecordNotFound 当"未迁移"；逐行标记使重复运行天然幂等。迁移移到 journal 写入线程启动之前，旧二进制留下的 journal 记录在提交时经 `LegacyCostFixer` 转换一次 | `TestR113MigrationRetryAtomic`（触发器注入失败，回滚后重试只除一次）、`TestLedgerUSDDisplayStampsOnly`、`TestLegacyCostFixer`、`TestCostFixerAppliedOnCommit` |
| R113-03 P2 | 快照恢复把禁用的价格变成启用 | 恢复价目在 Create 前记下 Enabled，Create 后显式写回 false | `TestR113RestoreDisabledPrice`（禁用行保持禁用、启用行恢复启用、禁用行不参与计价） |
| 并行评审 | Anthropic → Chat → Responses 串联流偶发丢缓存写 | `chainStream` 等第一段 goroutine 结束再合并用量（第二段看到终止事件时第一段可能尚未写入） | `TestR112CacheWritePricing` 第五条路径在 `-race` 下稳定 |
| 并行评审 | 部分已知费用显示成"未计价" | 四处日志视图：`cost_known=false` 且金额 >0 显示 "≥ 金额" 并提示"部分计价"，金额为 0 才显示"未计价" | 人工：让一次尝试 500 无 usage 后成功，日志费用显示 ≥ |
| 并行评审 | Postgres 恢复后不重置序列 | 恢复事务末尾按方言对 accounts / model_mappings / model_groups / model_prices 执行 `setval` | 代码审查；SQLite 不受影响 |

两轮评审的 `docs/review-repros/accept112/run.py`、`accept113/run.py` 全部通过；全量 `go test -race`、smoke 56、api-crud 90 通过。真实客户端与真实供应商的端到端验收仍未执行。

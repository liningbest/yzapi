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

## 1.0.15：第三轮复核 R114-01/02（`docs/acceptance-review-1.0.14-2026-09-11.md`）

| 编号 | 问题 | 修改 | 验收方式 |
|---|---|---|---|
| R114-01 P1 | `logstore.New` 内部已同步回放 journal，之后才装费用修正函数，旧记录原样入库且永远不再被改 | 修正函数改为构造参数 `logstore.WithCostFixer`，在打开 journal、首次 drain、后台协程之前安装；`SetCostFixer` 删除，不再存在与后台提交并发的 setter | `TestR114StartupReplay`（按主程序顺序：迁移 → 带修正函数构造 → 关闭，journal 里的 720 万微人民币入库为 100 万微美元并打标记）、`TestCostFixerAppliedOnCommit`（启动回放与后续提交都经过修正函数） |
| R114-02 P1 | 1.0.13 期间新产生的美元记录（两边都是美元、无逐行标记）被当作旧人民币尝试再除一次 | 迁移记录库的来源版本（`cost_ledger_origin`），对无标记行按证据逐行判定：尝试之和远大于请求金额的是 1.0.13 迁移过的旧行，按比例把尝试缩放到请求金额上（不依赖当时汇率）；两边一致且早于 1.0.13 迁移时刻的是回放进来的 1.0.12 记录，两边折算；其余只打标记。早期 1.0.14 库里无法判定的行标 `unverified` 并告警，不折算。journal 修正函数按同一来源规则，只对来自 1.0.12 的库、且早于迁移时刻的记录折算 | `TestLedgerV1Upgrade`（三类行各自结果）、`TestR114V113NewUSDRecords`（评审形状）、`TestLedgerV2UnverifiedRows`、`TestLegacyCostFixer`（来源 v0 折算、v1 不折算、迁移后创建的不折算） |

三轮评审的 `run.py` 全部通过（第三轮夹具里 `SetCostFixer` 改为构造参数写法，语义不变）。全量 `go test -race`、smoke 56、api-crud 90 通过。真实客户端、Postgres 多实例仍未实测。

## 1.0.16：第四轮复核 R115-01/02（`docs/acceptance-review-1.0.15-2026-09-11.md`）

| 编号 | 问题 | 修改 | 验收方式 |
|---|---|---|---|
| R115-01 P1 | 标了 `unverified` 的行仍按美元进入聚合与展示 | 状态贯穿全链路：`UsageHourly` 新增 `cost_unverified` 计数，`Aggregate` 对 unverified 行不累加任何金额、只计数（重建同样生效）；迁移把 1.0.14 回放时按美元记入小时汇总的金额按同一归属扣回并计数，同时置 `cost_known=false`；管理端 / 用户端日志 `cost=0`、`cost_unverified=true`，报表 `summary.cost_unverified` 与各分布项计数；四处日志视图显示"币种待核实"，用量页费用卡提示待核实条数 | `TestR115UnverifiedCostNotReported`（报表 cost=0、计数 1，管理端列表 / 详情与用户日志一致）、`TestLedgerV2UnverifiedRows`（小时汇总扣回、`Aggregate` 不计）、`TestAggregateUnverifiedCost` |
| R115-02 P1 | 来源不明的 journal 记录被直接标成 USD | 修正函数与库内迁移共用一套判定：v0 来源且早于迁移时刻的记录折算；v1 来源（1.0.13 自己的美元记录）标 USD；其余来源有金额的记录标 `unverified` 且 `cost_known=false`，无金额的标 USD | `TestR115UncertainJournal`、`TestLegacyCostFixer` |
| 并行评审 | 汇率 ≤ 3 时"尝试 / 请求 > 3"的判定不可靠 | 汇率小于 4 时，v1 来源早于迁移时刻且带尝试金额的行不再套用比例规则，直接标 `unverified` | 代码审查（现实汇率不会低于 4，作为保护） |

四轮评审的 `run.py`（6 + 3 + 2 + 2）全部通过；全量 `go test -race`、smoke 56、api-crud 90 通过。已被早期 1.0.15 回放错误标成 USD 的行（只可能存在于跑过 1.0.15 的库，实际只有本地演示库，其 journal 为空）没有可靠证据可追溯，不做猜测性补救。

## 1.0.17：第五轮复核 R116-01/02（`docs/acceptance-review-1.0.16-2026-09-11.md`）

| 编号 | 问题 | 修改 | 验收方式 |
|---|---|---|---|
| R116-01 P1 | 1.0.15 已标 `unverified` 的行，其金额仍留在小时汇总里；1.0.16 沿用 `usd-v3` 标记而直接跳过 | 标记升为 `usd-v4`；看到 `usd-v3` 时在同一事务里扫描所有 `unverified` 行，按当时入账的归属从小时汇总扣回并置 `cost_known=false`，随后推进标记；失败整体回滚，重启不重复扣回 | `TestR116UpgradeAlreadyUnverified`（触发器注入失败 → 回滚；重试 → 同一小时内已确认的 500 微美元保留、待核实计数 1；二次启动无变化；`Reconcile` 日志与汇总一致） |
| R116-02 P1 | 低汇率保护把 1.0.13 旧行标为待核实后，用仍是人民币的尝试金额从美元小时汇总里扣，出现负数 | 扣回改为"当时实际记入的金额"：来自 1.0.13 的库用按请求金额缩放后的尝试金额（小时汇总已随请求一起折成美元），其余来源用原始尝试金额；跨账号按各自尝试归属扣 | `TestR116V1UnbookUsesLedgerCurrency`（汇率 2、两账号尝试 50 万 / 150 万人民币、请求 100 万微美元：账号 1 扣 25 万、账号 2 扣 75 万，同小时另一笔 300 微美元保留，无负数） |
| 补充 | 对账只核对请求数与 Token | `Reconcile` 同时核对费用（美元账本，待核实行不计）与待核实条数 | `TestR116UpgradeAlreadyUnverified` 末尾 |

五轮评审的 `run.py`（6 + 3 + 2 + 2 + 2）全部通过；全量 `go test -race`、smoke 56、api-crud 90 通过。说明：1.0.16 也写 `usd-v3` 标记但已做扣回，若某库确实跑过 1.0.16（实际只有本地演示库，且没有 unverified 行），v4 步骤会再扣一次；1.0.16 未发布，故不为它单独区分。

## 1.0.18：第六轮复核 R117-01 与升级边界（`docs/acceptance-review-1.0.17-2026-09-11.md`）

| 编号 | 问题 | 修改 | 验收方式 |
|---|---|---|---|
| R117-01 P1 | 修理 v3 时读回 origin=v0，把已经是美元的小时汇总再除一次汇率 | 小时汇总整表折算只在库完全没有标记的第一次迁移执行（`!found`），任何后续步骤都不再碰；修理时若还有无标记行，不按来源猜测，一律标待核实 | `TestR117V3OriginV0MustNotRedivideHourly`（小时表保持 100 万微美元；无标记行标待核实） |
| 边界 | 跑过 1.0.16（已扣回）的库再升级会重复扣回 | 以 `cost_known` 作为逐行证据：只扣 `cost_known=true` 的待核实行（1.0.15 状态），1.0.16 处理过的行（已置 false）不再扣；1.0.15 期间本就是 false 且有金额的行没有证据，不动、日志报数，建议用保留期重建结算 | `TestR117V3AlreadyUnbookedMustNotSubtractAgain`、`TestR116UpgradeAlreadyUnverified` |
| 建议 | | 升级过 1.0.15 / 1.0.16 的库在升级后跑一次「计量维护 → 对账」，对账已核对费用与待核实条数，有差异按保留期重建 | 手工 |

六轮评审的 `run.py`（6 + 3 + 2 + 2 + 2 + 4）全部通过；全量 `go test -race`、smoke 56、api-crud 90 通过。

## 1.0.19：第七轮复核 R118-01（`docs/acceptance-review-1.0.18-canvas-chat.md`、`docs/acceptance-review-1.0.18-2026-09-11.md`）

两份复核都关闭了费用数据路径上的 R117 与"已扣回再扣"边界。剩余一条 P2：v3 修理成功后仍打"未剔除、请重建"告警，因为计数发生在把 `cost_known` 置 false 之后。现在在修理前计数，本趟结算的行不再被报；告警措辞改为"没有证据，先对账，有差异再重建"。回归：`TestR118RepairMustNotWarnAsUnsettled`（1.0.15 主路径静默、无证据行报数）。七轮评审的 `run.py`（6 + 3 + 2 + 2 + 2 + 4 + 2）全部通过；全量 `go test -race`、smoke 56、api-crud 90 通过。

复核结果（`docs/acceptance-review-1.0.19-canvas-chat.md`、`docs/acceptance-review-1.0.19-2026-09-11.md`）：R118-01 关闭，无新增阻断项；发布包 1.0.19 对应提交 `1c0dd0a` 的业务代码，本节之后只有文档变更。

## 1.0.20：Coding 场景性能——先补观测，不改热路径（`docs/coding-performance-changes-2026-09-11.md`）

| 修改 | 说明 | 验收方式 |
|---|---|---|
| 日志新增 `queue_wait_ms` | 等待网关 / 用户组 / Key 并发槽的时间；在取槽前后计时，失败也记录 | `TestTimingsRecorded` |
| 日志新增 `first_content_ms` | 首个带生成内容（正文、思考或工具调用增量）的事件写向客户端的时刻；按客户端协议判定，角色事件、空增量、心跳、仅 usage 不算；非流式为正文写出时刻；找到后不再解析事件 | `TestEventHasContentPerProtocol`（四协议 16 例）、`TestFirstContentWriterFiresOnce`（事件跨 Write 分片）、`TestTimingsRecorded`（同协议流、转换流、非流式） |
| `first_byte_ms` 改名展示 | 管理端与用户中心改为"上游响应头到达"并带说明，不再叫"首字节" | 界面 |
| 「最大重试次数」改为「最多尝试次数」 | 说明它是含首次的总次数 | 界面 |
| 文档 | `docs/coding-performance.md` 按评审修订；`docs/api.md` 补耗时口径 | |

实测（同协议 Anthropic 流式，零延迟 mock）：网关在 4 KB / 200 KB / 1 MB 请求体上分别多出约 0.3 / 2.9 / 12.9 ms 首字节时间，故本轮不改热路径。

## 1.0.21：Coding 性能观测的验收修正（`docs/coding-performance-acceptance-bd68104.md`、`docs/acceptance-review-coding-perf-canvas-chat.md`）

三个 P2 与材料缺口全部处理：首内容判定读取实际字段（空增量、签名、`null` 不算）；观测器只挂 SSE 分支，流式与非流式统一为"Write 成功返回之后"打点；`docs/api.md` 区分时间点与时长并收紧差值的解释；识别 CRLF 分隔；基准脚本 `scripts/bench-bigbody.sh` 与原始结果 `docs/perf/bigbody-2026-09-11.txt` 入库，含观测器开关前后对比（差异在噪声内）。评审夹具 `perf-bd68104/run.py` 通过。详见 `docs/coding-performance-changes-2026-09-11.md` 第 7 节。

## 1.0.22：Coding 性能观测第三版（`docs/coding-performance-acceptance-298cdd9.md`）

失败的 Write（含部分写入后出错）不再记录首内容；Responses 对象形 `delta` 也能判定；基准脚本记录二进制哈希、剔除非 200 样本、可记逐次样本；新增观测器微基准（首内容前每个非内容事件约 1.5 µs，之后直通 0 分配）；报告首段、第 5 节与第 3 节结论按验收收窄。详见 `docs/coding-performance-changes-2026-09-11.md` 第 8 节。

## 1.0.23：Coding 性能观测第四版（`docs/coding-performance-acceptance-d199ab8.md`）

工具调用名称事件计入首内容：Anthropic `content_block_start`（`tool_use` 且有 `name`）、Responses `response.output_item.added`（`function_call` 且有 `name`）；对象形 `delta` 递归检查非空字符串。新增端到端 `TestToolNameIsFirstContent`。详见 `docs/coding-performance-changes-2026-09-11.md` 第 9 节。

## 1.0.24：Coding 性能观测第五版（`docs/coding-performance-acceptance-5f8236e.md`）

对象形 Responses `delta` 只按载荷字段（`text` / `delta` / `arguments` / `partial_json` / `refusal` / `value` / `content`）判定，元数据字符串、数字、布尔不算；成对正反用例入库。详见 `docs/coding-performance-changes-2026-09-11.md` 第 10 节。

## 1.0.25：内置价目表按 2026-09-12 官网复核

原表停留在 2026-06，主力型号（GPT-6 Astra、GPT-5.6 Sol / Terra / Luna、GPT-5.5、Claude Fable 5.1 / Opus 5 / Sonnet 5、Gemini 3.x、Grok 4.6、DeepSeek V4、Kimi K3、GLM-5.x、Qwen 3.8、豆包 2.x 等）全部缺失。现按各供应商官网价格页逐家抓取重写，来源与抓取方式记在 `docs/pricing-sources-2026-09.md`；分档计价取最低档并在备注写明其他档，促销价与牌价在备注区分，官网不再列出的旧行保留并标"未复核"。`BuiltinUpdated` 改为 `2026-09`。

已部署实例：首次启动只补缺失的行，已有行（含你改过的）不动；要整体换成新表用「设置 → 计价 → 恢复内置」，它会重建全部内置行、保留自定义行。费用按上游模型名匹配，最长前缀优先（`gpt-5.5-pro` 命中自己的行而不是 `gpt-5.5`）。

## 1.0.26：客户端识别、接入指引、价目表同步（`docs/changes-2026-09-12-client-guide-priceimport.md`）

- 日志记录客户端标签（从 User-Agent 与识别头判断：claude-code、codex、gemini-cli、opencode、cline …）与 User-Agent；管理端日志可按客户端筛选，详情显示；用量报表新增按客户端分布（按明细日志统计，保留期内）。
- 用户中心新页「接入指引」：Claude Code / Codex / OpenCode / Gemini CLI / Cline / SDK 的可复制配置。
- 「设置 → 计价 → 同步价目」：从 LiteLLM、EasyCLIProxyAPI、自定义 URL 或上传文件导入，先预览后应用，手工行默认保留，内置行可恢复；价目行带来源与"已改"标记。

## 1.0.27：1.0.26 验收修复（`docs/changes-2026-09-12-client-guide-priceimport.md` 第 7 节）

- 价目同步改为「预览取得 plan_id → 应用只写预览过的内容」；EasyCLIProxyAPI 真实文件（对象形 `models`）可解析；每行过与手工新增相同的校验，无效行列出不写入；预览不再消耗配置快照；币种不同的行默认保留并在预览标出币种，勾选后才改；写库后运行态重载失败返回 503。
- 价格行 `(provider, pattern)` 唯一（小写存储，启动时去重并建唯一索引），新增 / 修改撞键 409。
- 客户端识别按整个产品名与 Referer 主机名匹配，不再用子串；`by_client` 与用量汇总使用同一小时窗口。
- 接入指引 OpenCode 示例改用 `{env:YZAPI_API_KEY}`。

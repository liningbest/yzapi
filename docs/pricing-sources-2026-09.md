# 内置价目表来源（2026-09-12 复核）

内置表 `internal/pricing/pricing.go` 的每一行都对应下列官网页面之一；未能在官网页面上再次找到的旧行保留并在备注里标"未复核"。所有价格为每百万 token，按各页面当天显示抄录；分档计价的模型取最低档并在备注写明其他档。

| 供应商 | 页面 | 抓取方式 | 说明 |
|---|---|---|---|
| OpenAI | https://developers.openai.com/api/docs/pricing | 直接抓取 | 标准档；`gpt-5.6-sol` 为促销价（至 2026-11-21）；`gpt-5.3-codex-spark` 官网无公开 API 价，按第三方汇总与 `gpt-5.3-codex` 同价 |
| Anthropic | https://platform.claude.com/docs/en/about-claude/pricing ；模型 ID 见 https://platform.claude.com/docs/en/about-claude/models/overview | 直接抓取 | 缓存写取 5 分钟档（1.25×）；Sonnet 5 的 $2/$10 官网已确认为正式价 |
| Google Gemini | https://ai.google.dev/gemini-api/docs/pricing | 直接抓取 | 付费档文本输入；页面注明现价有效至 2026-12-31，2027-01-01 起多数上调 |
| xAI | https://docs.x.ai/docs/models | 直接抓取 | <200K 档；≥200K 整条请求按 2 倍 |
| DeepSeek | https://api-docs.deepseek.com/quick_start/pricing | 直接抓取 | 高峰价；非高峰时段（UTC 01–04、06–10，工作日）减半 |
| Moonshot Kimi | https://platform.kimi.com/docs/pricing/chat | 直接抓取 | 人民币 |
| MiniMax | https://platform.minimax.cn/docs/guides/pricing-paygo | 直接抓取 | 人民币，标准档；M3 为"永久五折"后价 |
| 智谱 | https://docs.bigmodel.cn/cn/guide/start/pricing | 直接抓取 | 人民币，最低档 |
| 阿里云百炼 | https://help.aliyun.com/zh/model-studio/model-pricing | 直接抓取 | 人民币，中国区标准价、最低档；`qwen3.7-max` / `qwen3.7-plus` 官网当前有促销，表内填牌价并备注 |
| 火山方舟豆包 | https://ai.volcengine.com/model ；2.0 发布信息 https://news.qq.com/rain/a/20260214A04AJ700 | 官网价格页为前端渲染无法抓取，用 AI Hub 页面与发布稿 | 人民币，≤32K 档；2.1 系列与 Seed-Evolving 的缓存价官网未列，按 2.0 系列 20% 比例推算并备注 |

复核方法：每家只信官网自己的页面；第三方汇总只用来定位模型名，不用来定价，`gpt-5.3-codex-spark` 是唯一例外并已备注。下次复核时把本文件的日期和 `BuiltinUpdated` 一起更新。

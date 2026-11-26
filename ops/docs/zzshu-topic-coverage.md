# 万川帮助中心对照清单

基线 `69295d468a2598a2469fa674229a367762794bac`。参考 [吱吱鼠公开帮助中心](https://docs.zzshu.cc/) 的构建清单与可见导航，涵盖 37 个独立页面；这里采用万川原创文案、组件、模型及线路配置，保留现有指南与 API 参考。参考站资源、域名、条款和计费承诺不进入产品。万川现有 `/help` 路由对外兼容，独立营销 `/docs/` 不变。

| # | 参考路径 | 万川目标 | 交互及能力边界 |
| --- | --- | --- | --- |
| 1 | `/` | `/help` | 导航、专题、可搜索的文档首页 |
| 2 | `/integrations/cli/` | `/help/integrations` | 只生成配置，不自动调用付费模型 |
| 3 | `/integrations/cli/environment` | `/help/guides/cli-environment` | 根据本机运行环境排查 |
| 4 | `/integrations/cli/claude-code` | `/help/guides/claude-code` | 沿用万川 Messages 指南 |
| 5 | `/integrations/cli/codex` | `/help/guides/codex-cli` | 沿用万川 Responses 指南 |
| 6 | `/integrations/cli/kimi-code` | `/help/guides/kimi-code` | 自定义 provider，不替代订阅凭据 |
| 7 | `/integrations/cli/grok-build` | `/help/guides/grok-build` | 说明模型发现与推理接入区别；不承诺未证实的推理代理 |
| 8 | `/integrations/advanced/opencode` | `/help/guides/opencode` | 自定义兼容 provider |
| 9 | `/integrations/advanced/openclaw` | `/help/guides/openclaw` | 独立代理切换与回退 |
| 10 | `/integrations/advanced/hermes` | `/help/guides/hermes-agent` | 自定义兼容地址 |
| 11 | `/integrations/advanced/` | `/help/guides/desktop-overview` | 按客户端能力选协议 |
| 12 | `/integrations/advanced/claude-desktop` | `/help/guides/claude-desktop` | 不绕过客户端付费及组织限制 |
| 13 | `/integrations/advanced/workbuddy` | `/help/guides/workbuddy` | 仅支持手动接入；未新增一键导入 |
| 14 | `/integrations/advanced/aionui` | `/help/guides/aionui` | 区分直接自定义与外部 Agent |
| 15 | `/integrations/advanced/all-api-hub` | `/help/guides/all-api-hub` | 站点与模型凭据分用 |
| 16 | `/integrations/advanced/chatgpt-claude-code` | `/help/guides/gpt-claude-code` | 仅支持 Messages 的万川路由 |
| 17 | `/integrations/advanced/deepseek-claude-code` | `/help/guides/deepseek-claude-code` | 核对 Messages 支持 |
| 18 | `/integrations/advanced/deepseek-codex` | `/help/guides/deepseek-codex` | 核对 Responses 支持 |
| 19 | `/integrations/cc-switch/` | `/help/guides/cc-switch` | 沿用独立导入入口 |
| 20 | `/integrations/cc-switch/common` | `/help/guides/cc-switch-common` | 确认对应协议与凭据 |
| 21 | `/integrations/cc-switch/claude-code` | `/help/guides/cc-switch-claude-code` | 选 Claude Code、重开会话 |
| 22 | `/integrations/cc-switch/codex` | `/help/guides/cc-switch-codex` | 虚拟提供商支持 Responses 才适用 |
| 23 | `/integrations/cc-switch/codex-app` | `/help/guides/cc-switch-codex-app` | 核对桌面版配置来源 |
| 24 | `/integrations/cc-switch/claude-desktop` | `/help/guides/cc-switch-claude-desktop` | 验证客户端与组织策略支持 |
| 25 | `/integrations/cc-switch/usage-query` | `/help/guides/cc-switch-usage` | 账务以万川为准；不共享管理凭据 |
| 26 | `/integrations/cc-switch/cli` | `/help/guides/cc-switch-cli` | 社区客户端，不宣称是桌面版组件 |
| 27 | `/integrations/paint/` | `/help/guides/images-overview` | Gemini 与 OpenAI 两类入口 |
| 28 | `/integrations/paint/banana` | `/help/guides/gemini-images` | 按万川实际图像模型与响应 |
| 29 | `/integrations/paint/gpt-image` | `/help/guides/openai-images` | 生成、编辑分开 |
| 30 | `/faq/` | `/help/guides/faq` | 逐层分辨地址、认证、模型、费用 |
| 31 | `/faq/claude-code` | `/help/guides/faq-claude-code` | 环境优先级、工具退路 |
| 32 | `/faq/codex` | `/help/guides/faq-codex` | 端点与桌面环境 |
| 33 | `/faq/gemini` | `/help/guides/faq-gemini` | 原生与兼容格式区别 |
| 34 | `/integrations/cli/cache-fix` | `/help/guides/cli-cache-fix` | 保留证书校验与可恢复数据 |
| 35 | `/guides/overview` | `/help/guides/quick-start` | 登录、API Key、钱包、模型与示例 |
| 36 | `/api/` | `/help/api` | 三协议基础地址与六常用端点；保留 26 方法＋路径和旧深链 |
| 37 | `/legal` | `/help/guides/terms-and-policies` | 只链接万川当前条款、隐私、工单；无新增法律承诺 |

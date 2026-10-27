/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { GuideDoc } from '../types'

export const ccSwitch: GuideDoc = {
  slug: 'cc-switch',
  title: 'CC Switch 一键配置',
  description:
    '用开源工具 CC Switch 把本站保存为供应商，在多个中转站之间一键切换。',
  updated: '2026-08-30',
  body: `
> CC Switch 是第三方开源桌面工具（[GitHub](https://github.com/farion1231/cc-switch)），用于管理 Claude Code / Codex CLI 的供应商配置并一键切换。它把你手动改环境变量、改 config.toml 的工作变成了点选。

## 适用场景

- 同时使用多个 API 供应商，需要频繁切换。
- 不想手动维护 \`~/.codex/config.toml\` 或 shell 环境变量。
- 团队内希望统一分发一份供应商配置。

只接一个站、且愿意手动配置的话，直接看 [接入 Claude Code](/help/guides/claude-code) 或 [接入 Codex CLI](/help/guides/codex-cli) 即可，不必额外装工具。

## 配置步骤

1. 从 GitHub Releases 下载并安装 CC Switch。
2. 在 [控制台 → API 令牌](/keys) 创建令牌。
3. 在 CC Switch 中新增供应商，填三项：
   - **名称**：万川（任意）
   - **Base URL / 接口地址**：Claude Code 供应商填 \`{{BASE_URL}}\`（不带 /v1）；Codex 供应商填 \`{{BASE_URL}}/v1\`
   - **API Key**：控制台创建的令牌
4. 把新供应商设为当前生效项，重启 Claude Code / Codex CLI 生效。

模型名填 [定价页](/pricing) 中的真实 model id，不要沿用其他平台的名称。

## 验证

切换后先发一条最小请求确认（也可以直接在 [文本生成](/playground) 验证账号侧就绪）：

\`\`\`bash
curl {{BASE_URL}}/v1/messages \\
  -H "x-api-key: $WANCHUAN_API_KEY" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"MODEL_ID","max_tokens":64,"messages":[{"role":"user","content":"你好"}]}'
\`\`\`

## 常见问题

- **切换后客户端仍连旧供应商**：客户端进程没重启，或 shell 里残留了手动 export 的 \`ANTHROPIC_BASE_URL\` 等变量覆盖了工具写入的配置。
- **404**：Base URL 带没带 \`/v1\` 与客户端类型不匹配，见上面第 3 步的区分。
- **401 / 403**：令牌拼错或分组不含该模型，见 [认证与密钥](/help/guides/authentication)。

本页基于 2026-08-30 校订。CC Switch 为第三方工具，界面与字段名随版本变化，以其官方文档为准；本站与该工具无隶属关系。
`.trim(),
}

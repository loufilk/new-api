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

export const claudeCode: GuideDoc = {
  slug: 'claude-code',
  title: '接入 Claude Code',
  description: '两个环境变量即可让 Claude Code 使用本站。',
  recommended: true,
  updated: '2026-08-30',
  body: `
> 注意这里的 Base URL 是 \`{{BASE_URL}}\`，**不带 /v1**。Claude Code 会自己拼上 \`/v1/messages\`，多写一层会得到 404。这和 OpenAI SDK 需要带 \`/v1\` 正好相反。

Claude Code 走 Anthropic Messages 协议。本站提供 \`/v1/messages\` 端点，因此只需要改两个环境变量，不用改工作流。

## 前置条件

- 已在 [控制台 → API 令牌](/keys) 创建令牌，账户余额充足。创建方式见 [快速开始](/help/guides/quick-start)。
- 本机已安装 Claude Code 并能正常启动。

## 配置步骤

1. 把密钥写入环境变量 \`WANCHUAN_API_KEY\`，不要硬编码进 shell 配置文件。
2. 从 [定价页](/pricing) 复制一个真实 model id，替换下面的 \`MODEL_ID\`。
3. 设置三个环境变量后启动 Claude Code。

\`\`\`bash
# macOS / Linux
export ANTHROPIC_BASE_URL="{{BASE_URL}}"
export ANTHROPIC_AUTH_TOKEN="$WANCHUAN_API_KEY"
export ANTHROPIC_MODEL="MODEL_ID"

claude
\`\`\`

\`\`\`powershell
# Windows PowerShell
$env:ANTHROPIC_BASE_URL = "{{BASE_URL}}"
$env:ANTHROPIC_AUTH_TOKEN = "$env:WANCHUAN_API_KEY"
$env:ANTHROPIC_MODEL = "MODEL_ID"

claude
\`\`\`

想固定下来就把前三行写进 \`~/.zshrc\`、\`~/.bashrc\` 或 PowerShell 配置文件；只想临时试一次，直接在当前终端里执行即可。嫌手动配置麻烦，也可以用 [CC Switch 一键配置](/help/guides/cc-switch)。

## 验证连通

启动 Claude Code 之前先单独打一次接口。这样能把「网关配置问题」和「客户端配置问题」分开：

\`\`\`bash
curl {{BASE_URL}}/v1/messages \\
  -H "x-api-key: $WANCHUAN_API_KEY" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "MODEL_ID",
    "max_tokens": 64,
    "messages": [{"role": "user", "content": "你好"}]
  }'
\`\`\`

返回 200 且带有模型回答，说明 Base URL、密钥与 model id 三者都对，剩下的问题就只可能出在 Claude Code 自身的配置上。

## 常见问题

- **404**：多半是 \`ANTHROPIC_BASE_URL\` 后面多写了 \`/v1\`，变成了 \`/v1/v1/messages\`；也可能是 model id 不在目录里。
- **401**：密钥没生效。检查环境变量是否在当前 shell 里真的展开了，以及是否误把 \`Bearer \` 前缀写进了密钥值。
- **403**：密钥有效，但令牌分组不包含该模型。换一个目录中可用的 model id 再试。
- **429**：触发限流。降低并发后重试，重试策略见 [错误码与排障](/help/guides/troubleshooting)。
- **官方账号登录态干扰**：如果之前登录过 Claude 官方账号，客户端可能优先用已保存的凭据。退出登录后再用环境变量方式启动。

## 相关文档

- [接入 Anthropic SDK](/help/guides/anthropic-sdk)——同一套 Messages 协议的程序化调用方式。
- [计费与赠金说明](/help/guides/billing)——看懂账单金额。

本页基于 2026-08-30 的 Claude Code 版本校订。客户端环境变量名可能随版本调整，如与官方文档不一致，以官方为准。
`.trim(),
}

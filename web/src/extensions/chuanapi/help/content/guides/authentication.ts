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

export const authentication: GuideDoc = {
  slug: 'authentication',
  title: '认证与密钥',
  description: 'Authorization Bearer、密钥管理与 401 / 403 排查。',
  updated: '2026-08-30',
  body: `
> 密钥只在 [控制台 → API 令牌](/keys) 创建与吊销；万川不会通过任何其他渠道向你索要密钥。

万川使用与 OpenAI 一致的 Bearer 认证，迁移时通常只需替换 Base URL 与密钥两处。

## 请求头格式

每个请求都需要带 \`Authorization\` 头，值为 \`Bearer\` 加空格再加密钥本身。JSON 请求体还需要 \`Content-Type\`。

\`\`\`http
Authorization: Bearer $WANCHUAN_API_KEY
Content-Type: application/json
\`\`\`

不要在密钥前后加引号，也不要把 \`Bearer\` 写进控制台复制出来的密钥字符串里——重复的前缀是最常见的 401 来源。

## 密钥在哪里创建

密钥在 [控制台 → API 令牌](/keys) 创建和管理，可设置额度、分组与过期时间，调用明细见 [使用日志](/usage-logs)。

一个账号可以创建多个密钥。建议按用途分开，例如本地开发、CI、线上服务各用一个，出问题时可以单独吊销而不影响其他环境。

## 密钥放在哪里

用环境变量注入，不要写进源码或提交进仓库。

\`\`\`bash
# macOS / Linux
export WANCHUAN_API_KEY="替换为控制台创建的密钥"

# Windows PowerShell
$env:WANCHUAN_API_KEY = "替换为控制台创建的密钥"
\`\`\`

官方 SDK 从环境变量读取即可，只需覆盖 base_url：

\`\`\`python
from openai import OpenAI
import os

client = OpenAI(
    api_key=os.environ["WANCHUAN_API_KEY"],
    base_url="{{BASE_URL}}/v1",
)
\`\`\`

特别注意：**不要把密钥放进浏览器端代码**。前端直连会把密钥暴露给任何访客。需要在网页里调用时，应由你自己的后端持有密钥并代理请求。

## 验证密钥是否可用

最省事的自检是请求 \`/v1/models\`，它只读取账号可见的目录，不产生模型调用费用。

\`\`\`bash
curl -sS {{BASE_URL}}/v1/models \\
  -H "Authorization: Bearer $WANCHUAN_API_KEY" \\
  -o /dev/null -w "%{http_code}\\n"

# 200 = 密钥可用
# 401 = 密钥缺失、拼写错误或已吊销
\`\`\`

该接口在未认证时返回 401，因此它同时验证了两件事：密钥格式被正确解析，且密钥仍然有效。

## 401 与 403 的区别

- **401**：认证没通过。密钥缺失、拼错、带了多余前缀，或已被吊销。先用上面的 \`/v1/models\` 自检。
- **403**：认证通过但没有权限。通常是令牌分组不包含目标模型，或该请求来源被策略拒绝。换一个目录中确认可用的 model id 再试，可以快速区分这两类。

两者都不该用重试解决——重试同一个无效密钥只会重复失败。详细分类见 [错误码与排障](/help/guides/troubleshooting)。

## 轮换与吊销

1. 在控制台创建新密钥。
2. 更新环境变量或密钥管理服务，滚动重启使用方。
3. 确认新密钥已在 [使用日志](/usage-logs) 中产生调用，再吊销旧密钥。

怀疑泄露时应立即吊销，而不是先改用途。密钥一旦进入公开仓库或客户端产物，就必须视为已泄露。
`.trim(),
}

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

export const codexCli: GuideDoc = {
  slug: 'codex-cli',
  title: '接入 Codex CLI',
  description: '配置 config.toml，让 Codex CLI 使用本站。',
  updated: '2026-08-30',
  body: `
> 这里的 \`base_url\` 需要带 \`/v1\`，即 \`{{BASE_URL}}/v1\`。Codex CLI 会在其后拼接 \`/chat/completions\` 或 \`/responses\`。

Codex CLI 支持自定义 model provider。把 base_url 指向本站即可，其余工作流不变。

## 前置条件

- 已在 [控制台 → API 令牌](/keys) 创建令牌并写入环境变量 \`WANCHUAN_API_KEY\`。
- 本机已安装 Codex CLI，且存在 \`~/.codex\` 目录。

## 配置步骤

编辑 \`~/.codex/config.toml\`，新增一个指向本站的 provider，并把默认 provider 切过去：

\`\`\`toml
# ~/.codex/config.toml
model = "MODEL_ID"
model_provider = "wanchuan"

[model_providers.wanchuan]
name = "WanChuan"
base_url = "{{BASE_URL}}/v1"
env_key = "WANCHUAN_API_KEY"
wire_api = "chat"
\`\`\`

把 \`MODEL_ID\` 换成 [定价页](/pricing) 里的真实 id。\`env_key\` 填的是环境变量名而不是密钥本身，密钥不会落到配置文件里。

## chat 还是 responses

本站两个端点都提供，按模型和你的使用方式选：

- \`wire_api = "chat"\`——走 \`/v1/chat/completions\`，兼容面最广，绝大多数模型都支持，建议先用这个。
- \`wire_api = "responses"\`——走 \`/v1/responses\`，部分推理模型在该协议下能力更完整。遇到工具调用或推理过程表现异常时可以换过来试。

\`\`\`toml
[model_providers.wanchuan]
name = "WanChuan"
base_url = "{{BASE_URL}}/v1"
env_key = "WANCHUAN_API_KEY"
wire_api = "responses"
\`\`\`

两个端点的语义边界见 [协议兼容总览](/help/guides/compatibility)。

## 验证连通

改完配置先别急着启动 codex，单独打一次接口更容易定位问题：

\`\`\`bash
curl {{BASE_URL}}/v1/chat/completions \\
  -H "Authorization: Bearer $WANCHUAN_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"MODEL_ID","messages":[{"role":"user","content":"你好"}]}'
\`\`\`

## 常见问题

- **404**：\`base_url\` 漏了 \`/v1\`，或 model id 不在目录里。
- **401**：\`env_key\` 指向的环境变量在启动 codex 的那个 shell 里没有值。注意 GUI 终端与登录 shell 的环境可能不同。
- **403**：令牌分组不含该模型，换一个目录中可用的 id。
- **工具调用不生效**：先确认该模型支持工具调用，再尝试把 \`wire_api\` 切到 \`responses\`。
- **配置没被读取**：确认改的是 \`~/.codex/config.toml\` 而不是项目内的同名文件，且 TOML 语法正确（表头拼写错误会被静默忽略）。

## 相关文档

- [协议兼容总览](/help/guides/compatibility)——Chat Completions 与 Responses 的边界。
- [错误码与排障](/help/guides/troubleshooting)——状态码含义与重试策略。

本页基于 2026-08-30 的 Codex CLI 配置格式校订。该 CLI 的配置字段随版本变动较快，如与官方文档不一致，以官方为准。
`.trim(),
}

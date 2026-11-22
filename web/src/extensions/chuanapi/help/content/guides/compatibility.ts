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

export const compatibility: GuideDoc = {
  slug: 'compatibility',
  title: '协议兼容总览',
  description:
    'OpenAI / Anthropic / Gemini 协议的端点边界、Chat Completions 与 Responses 的差异与客户端排查。',
  updated: '2026-08-30',
  body: `
> 「OpenAI 兼容」指调用协议兼容，不等于 OpenAI 的全部接口都存在。账号、组织与 Assistants 等管理面接口不在兼容范围内。

## 迁移只需改两处

\`\`\`python
# 迁移前
client = OpenAI(api_key=OPENAI_KEY)

# 迁移后：只改这两行
client = OpenAI(
    api_key=os.environ["WANCHUAN_API_KEY"],
    base_url="{{BASE_URL}}/v1",
)
\`\`\`

主入口：\`{{BASE_URL}}/v1\`，末尾的 \`/v1\` 不能省略。

## 三种协议入口

| 协议 | 路径 | 典型客户端 |
| --- | --- | --- |
| OpenAI Chat Completions | \`/v1/chat/completions\` | OpenAI SDK 及多数兼容客户端 |
| OpenAI Responses | \`/v1/responses\` | Codex CLI 等 Responses 风格工具 |
| Anthropic Messages | \`/v1/messages\` | Claude Code / Anthropic SDK |
| Gemini 原生 | \`/v1beta/models/{model}:generateContent\` | Gemini 生态客户端 |

完整端点清单与请求 / 响应示例见 [API 参考](/help/api)。注意 Anthropic 协议客户端（如 Claude Code）配置的 Base URL 是 \`{{BASE_URL}}\`，**不带** \`/v1\`——客户端自己会拼 \`/v1/messages\`。

## Chat Completions 与 Responses 的区别

两者都是 OpenAI 风格接口，但形态不同。\`/v1/chat/completions\` 用一个 messages 数组表达完整对话，是目前最通用的选择；\`/v1/responses\` 面向以「一次任务」为单位的工作流，被 Codex 一类工具使用。

不确定用哪个时选 Chat Completions——它的客户端生态最广。

## 兼容边界

- 不提供 OpenAI 的组织管理、Assistants 等管理面接口。
- 模型能力（工具调用、视觉、推理）随模型而异，以 [定价页](/pricing) 该模型的标注为准，不能假设所有模型都具备。
- 模型名不通用。必须使用本站目录中的 model id，照搬其他平台的名称会返回 404。
- 优先使用标准 \`Authorization: Bearer\` 认证；自定义 Header 不保证透传。
- \`max_tokens\` 等参数的上限随模型而变，超出会返回 400。

## 客户端排查

1. 确认客户端真的允许自定义 base URL，且没有把路径写死成其他厂商域名。
2. 确认 base URL 带 \`/v1\`；有些客户端会自行追加，重复后会变成 \`/v1/v1\` 而 404。
3. 用 [快速开始](/help/guides/quick-start) 里的 curl 先验证账号与模型可用，再排查客户端本身。
4. 状态码含义见 [错误码与排障](/help/guides/troubleshooting)。
`.trim(),
}

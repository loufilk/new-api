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

export const anthropicSdk: GuideDoc = {
  slug: 'anthropic-sdk',
  title: '接入 Anthropic SDK',
  description: '用官方 Anthropic SDK 调用 Claude 格式接口。',
  updated: '2026-08-30',
  body: `
> \`base_url\` 填 \`{{BASE_URL}}\`，**不要带 /v1**。Anthropic SDK 会自己拼上 \`/v1/messages\`。这是从 OpenAI SDK 迁过来时最容易踩的坑。

本站提供 Anthropic Messages 端点。官方 SDK 只需改 base_url 与密钥，但 Base URL 的写法和 OpenAI SDK 正好相反。

## Base URL 的差异

两个 SDK 的拼接行为不同，写错就是 404：

- OpenAI SDK：\`{{BASE_URL}}/v1\` → 实际请求 \`/v1/chat/completions\`
- Anthropic SDK：\`{{BASE_URL}}\` → 实际请求 \`/v1/messages\`

## Python

\`\`\`python
# pip install anthropic
import os
from anthropic import Anthropic

client = Anthropic(
    api_key=os.environ["WANCHUAN_API_KEY"],
    base_url="{{BASE_URL}}",
)

resp = client.messages.create(
    model="MODEL_ID",
    max_tokens=1024,
    messages=[{"role": "user", "content": "你好"}],
)
print(resp.content[0].text)
\`\`\`

把 \`MODEL_ID\` 换成 [定价页](/pricing) 里的真实 id。

## Node.js

\`\`\`javascript
// npm i @anthropic-ai/sdk
import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic({
  apiKey: process.env.WANCHUAN_API_KEY,
  baseURL: "{{BASE_URL}}",
});

const resp = await client.messages.create({
  model: "MODEL_ID",
  max_tokens: 1024,
  messages: [{ role: "user", content: "你好" }],
});
console.log(resp.content[0].text);
\`\`\`

## 与官方端点的差异

- **\`max_tokens\` 必填**：Messages 协议要求显式给出，省略会返回 400。OpenAI 协议下它是可选的。
- **\`system\` 是顶层参数**：不放进 \`messages\` 数组。
- **model id 用本站目录的**：不要沿用官方的模型名，目录之外的名称一律 404。
- **计费口径统一**：无论走 Messages 还是 Chat Completions，都按本站定价计费，不因协议不同而变化。

\`\`\`python
resp = client.messages.create(
    model="MODEL_ID",
    max_tokens=1024,
    system="你是一个简洁的技术助手。",
    messages=[{"role": "user", "content": "解释一下 SSE"}],
)
\`\`\`

## 用 curl 验证

SDK 报错时先用 curl 排除客户端因素：

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

## 常见问题

- **404**：\`base_url\` 多写了 \`/v1\`，实际打到了 \`/v1/v1/messages\`。
- **400 max_tokens**：忘了传 \`max_tokens\`，或者填的值超过该模型上限。
- **401**：密钥没读到，或者环境里残留了 \`ANTHROPIC_API_KEY\` 覆盖了显式传入的值。
- **403**：令牌分组不含该模型。

## 相关文档

- [接入 Claude Code](/help/guides/claude-code)——同一个端点的终端客户端用法。
- [接入 OpenAI SDK](/help/guides/openai-sdk)——兼容面更广的另一条路。

本页基于 2026-08-30 校订。
`.trim(),
}

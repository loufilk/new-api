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

export const openaiSdk: GuideDoc = {
  slug: 'openai-sdk',
  title: '接入 OpenAI SDK',
  description: '用官方 OpenAI SDK 在 Python 或 JavaScript 中调用本站。',
  recommended: true,
  updated: '2026-08-30',
  body: `
> \`base_url\` 必须带 \`/v1\`，即 \`{{BASE_URL}}/v1\`。SDK 会在其后拼接 \`/chat/completions\`。这一点和 Anthropic SDK 不同，后者不要带 \`/v1\`。

官方 SDK 直接可用。已有项目改两处配置就能迁过来，业务代码一行不用动。

## Python

\`\`\`python
# pip install openai
import os
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["WANCHUAN_API_KEY"],
    base_url="{{BASE_URL}}/v1",
)

resp = client.chat.completions.create(
    model="MODEL_ID",
    messages=[{"role": "user", "content": "你好"}],
)
print(resp.choices[0].message.content)
\`\`\`

把 \`MODEL_ID\` 换成 [定价页](/pricing) 里的真实 id。也可以直接列出当前密钥可见的模型：

\`\`\`python
for model in client.models.list().data:
    print(model.id)
\`\`\`

## Node.js

\`\`\`javascript
// npm i openai
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.WANCHUAN_API_KEY,
  baseURL: "{{BASE_URL}}/v1",
});

const resp = await client.chat.completions.create({
  model: "MODEL_ID",
  messages: [{ role: "user", content: "你好" }],
});
console.log(resp.choices[0].message.content);
\`\`\`

注意 Node SDK 的字段名是 \`baseURL\`（驼峰、URL 全大写），Python 是 \`base_url\`。拼错不会报错，只会连到默认的官方域名。

## 流式与工具调用

写法与官方完全一致，加 stream 参数即可：

\`\`\`python
stream = client.chat.completions.create(
    model="MODEL_ID",
    messages=[{"role": "user", "content": "写一首短诗"}],
    stream=True,
)
for chunk in stream:
    delta = chunk.choices[0].delta.content
    if delta:
        print(delta, end="", flush=True)
\`\`\`

工具调用同理，\`tools\` 与 \`tool_choice\` 语义不变。详见 [流式输出](/help/guides/streaming) 与 [工具调用](/help/guides/function-calling)。

## 常见问题

- **请求打到了官方而不是本站**：多半是 Node 里写成了 \`base_url\`，或者环境里还留着 \`OPENAI_BASE_URL\`。SDK 的显式参数优先级高于环境变量，但拼错的字段会被静默忽略。
- **404**：\`base_url\` 漏了 \`/v1\`，或 model id 不在目录里。
- **401**：环境变量没读到。检查是不是把 \`OPENAI_API_KEY\` 和本站的令牌搞混了。
- **超时**：长输出建议开流式，或调大 SDK 的 \`timeout\`。默认超时对推理模型往往偏短。

## 相关文档

- [协议兼容总览](/help/guides/compatibility)——Chat Completions 与 Responses 的边界。
- [接入 Anthropic SDK](/help/guides/anthropic-sdk)——需要 Messages 协议时用这个。

本页基于 2026-08-30 校订。
`.trim(),
}

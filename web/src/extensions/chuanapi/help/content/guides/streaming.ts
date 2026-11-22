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

export const streaming: GuideDoc = {
  slug: 'streaming',
  title: '流式输出',
  description: 'SSE 增量解析、首字延迟、空闲超时与中断传输注意事项。',
  updated: '2026-08-30',
  body: `
> 流式响应先返回 200 再逐块推送，因此「请求成功」不等于「生成成功」。故障可能出现在已经收到部分内容之后，必须在解析循环里单独处理。

流式把一次生成拆成多个增量推送，首字更快、长回答体感更好。协议与 OpenAI 一致，客户端无需改造。

## 开启流式

在请求体里加 \`"stream": true\` 即可。其余参数与非流式一致，返回形态从一个完整 JSON 变成一串 SSE 事件。

## SSE 数据格式

每个事件以 \`data: \` 开头，后接一个 JSON 片段，事件之间用空行分隔。文本增量在 \`choices[0].delta.content\` 里。

\`\`\`text
data: {"choices":[{"delta":{"role":"assistant","content":""}}]}

data: {"choices":[{"delta":{"content":"SSE"}}]}

data: {"choices":[{"delta":{"content":" 是"}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

data: [DONE]
\`\`\`

有三点容易踩坑：首个事件的 \`delta\` 可能只带角色而没有内容；结束前会出现带 \`finish_reason\` 而内容为空的事件；最后的 \`[DONE]\` 不是合法 JSON，直接丢给解析器会抛错，需要先判断再解析。

## 用 SDK 消费增量

官方 SDK 已经处理了分帧与 [DONE]，直接迭代即可。

\`\`\`python
from openai import OpenAI

client = OpenAI(api_key="...", base_url="{{BASE_URL}}/v1")

stream = client.chat.completions.create(
    model="MODEL_ID",
    messages=[{"role": "user", "content": "用三句话解释 SSE"}],
    stream=True,
)

for chunk in stream:
    delta = chunk.choices[0].delta
    if delta.content:
        print(delta.content, end="", flush=True)
\`\`\`

\`\`\`javascript
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.WANCHUAN_API_KEY,
  baseURL: "{{BASE_URL}}/v1",
});

const stream = await client.chat.completions.create({
  model: "MODEL_ID",
  messages: [{ role: "user", content: "用三句话解释 SSE" }],
  stream: true,
});

for await (const chunk of stream) {
  process.stdout.write(chunk.choices[0]?.delta?.content ?? "");
}
\`\`\`

注意 \`delta.content\` 可能为 \`None\` 或 \`undefined\`，拼接前要判空，否则会把 \`undefined\` 输出到用户界面上。

## 用 curl 观察原始流

排查时建议直接看原始流。关键是 \`-N\` 关闭缓冲，否则会等到全部结束才一次性输出，看不出增量行为。

\`\`\`bash
curl -N {{BASE_URL}}/v1/chat/completions \\
  -H "Authorization: Bearer $WANCHUAN_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "MODEL_ID",
    "stream": true,
    "messages": [{"role": "user", "content": "用三句话解释 SSE"}]
  }'
\`\`\`

## 超时与中断

- 不要用「整体请求超时」约束流式调用。长回答的总时长天然较长，应该用「两个增量之间的间隔」做空闲超时。
- 反向代理和网关的缓冲会破坏流式体感。自建一层转发时需要关闭响应缓冲。
- 用户主动取消时要真正断开连接，否则生成会继续进行并继续计费。
- 中途断流不会改变已返回的 200 状态码。整体重试会让用户看到重复的前半段，处理策略见 [错误码与排障](/help/guides/troubleshooting)。

## 流式下的用量统计

流式增量里通常不携带完整的 token 用量，因此不要依赖最后一个事件来记账。实际扣费以 [使用日志](/usage-logs) 为准；本地若需要统计，应在流结束后按累计文本自行估算，并明确标注为估算值。换算规则见 [计费与赠金说明](/help/guides/billing)。
`.trim(),
}

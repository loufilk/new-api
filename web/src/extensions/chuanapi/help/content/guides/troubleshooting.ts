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

export const troubleshooting: GuideDoc = {
  slug: 'troubleshooting',
  title: '错误码与排障',
  description:
    '401 / 402 / 403 / 404 / 429 / 5xx 在本站分别意味着什么，以及如何解决。',
  updated: '2026-08-30',
  body: `
> 状态码只说明「哪一类问题」，具体原因在响应体里。排查时务必把 response body 一起打出来，只看状态码往往不足以定位。

先按状态码分成「改请求」和「等一下再试」两类，再决定要不要重试。对不该重试的错误做重试只会放大问题。

## 状态码速查

| 状态码 | 含义 | 处理方式 | 重试 |
| --- | --- | --- | --- |
| 400 | 请求体不合法：字段缺失、类型错误，或参数超出模型允许范围 | 对照报错字段修请求，常见是 \`max_tokens\` 超过该模型的最大输出 | 不应重试 |
| 401 | 认证未通过：令牌缺失、拼写错误、带了多余前缀，或已删除 | 按 [认证与密钥](/help/guides/authentication) 自检 | 不应重试 |
| 402 | 余额不足，无法继续计费调用 | 到 [钱包](/wallet) 充值后再试 | 不应重试 |
| 403 | 认证通过但无权限：令牌分组不含该模型，或来源被策略拒绝 | 换用 [定价页](/pricing) 中确认可用的 model id 验证 | 不应重试 |
| 404 | 路径或 model id 不存在 | 确认 Base URL 带 \`/v1\`，且 model id 来自当前公开目录 | 不应重试 |
| 413 | 请求体过大（常见于 base64 大图） | 压缩图片或改用 URL 传图，见 [视觉输入](/help/guides/vision) | 不应重试 |
| 429 | 请求过于密集，或触达上游速率约束 | 降低并发并按指数退避重试 | 可重试 |
| 5xx | 网关或上游临时故障 | 退避重试；持续复现则记录时间与 model id 反馈 | 可重试 |

## 哪些该重试，哪些不该

分界线很简单：**请求本身有问题就不要重试**（400、401、402、403、404、413），**环境暂时不可用才重试**（429、5xx）。

对 401 或 404 做重试是常见的错误设计：它不会自己变好，只会把一次失败放大成几十次，在 429 的场景下还会让情况更糟。

## 退避重试示例

重试要带指数退避和随机抖动。多个 worker 同时失败又同时重试，会形成同步的冲击波峰。

\`\`\`python
import time, random
from openai import OpenAI, APIStatusError

client = OpenAI(api_key="...", base_url="{{BASE_URL}}/v1")

RETRIABLE = {429, 500, 502, 503, 504}

def chat_with_retry(**kwargs):
    for attempt in range(5):
        try:
            return client.chat.completions.create(**kwargs)
        except APIStatusError as err:
            if err.status_code not in RETRIABLE or attempt == 4:
                raise
            # 指数退避 + 抖动，避免同时重试造成二次冲击
            delay = min(2 ** attempt, 8) + random.random()
            time.sleep(delay)
\`\`\`

## 流式请求中途失败

流式响应先返回 200，再逐块推送，所以故障可能出现在已经收到部分内容之后。这种情况不会体现在状态码上，需要在解析循环里单独处理连接中断。

直接整体重试会让用户看到重复的前半段。可行做法是把已收到的增量缓存下来，重试后只追加新内容，或者明确丢弃这次结果重新生成。相关注意事项见 [流式输出](/help/guides/streaming)。

## 定位一次具体失败

\`\`\`bash
# 保留响应体：错误信息通常在 body 里，状态码本身不够定位
curl -sS {{BASE_URL}}/v1/chat/completions \\
  -H "Authorization: Bearer $WANCHUAN_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"MODEL_ID","messages":[{"role":"user","content":"ping"}]}' \\
  -D - -o -
\`\`\`

1. 确认 Base URL 是否为 \`{{BASE_URL}}/v1\`，漏掉 \`/v1\` 会得到 404。
2. 确认 model id 来自 [定价页](/pricing) 且令牌分组可用。
3. 在 [使用日志](/usage-logs) 中定位这次请求，核对扣费与上游返回。
`.trim(),
}

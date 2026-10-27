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

export const vision: GuideDoc = {
  slug: 'vision',
  title: '视觉输入',
  description: '图文混排消息结构、URL 与 base64 传图、多图与成本权衡。',
  updated: '2026-08-30',
  body: `
> 视觉输入指「看图回答」。它和图像生成是两件不同的事——生成类模型走各自的端点与计费方式，请在 [定价页](/pricing) 确认。

带图请求把 \`content\` 从字符串换成数组，用内容块混排文字与图片。协议与 OpenAI 一致。

## 哪些模型支持

能力随模型而异，以 [定价页](/pricing) 该模型的标注为准。向不支持视觉的模型传图片块，通常会报参数错误或图片被静默忽略。

## 消息体结构

纯文本请求里 \`content\` 是字符串；带图时改成数组，每个元素是一个内容块，\`type\` 为 \`text\` 或 \`image_url\`。顺序会影响理解，建议先给指令再给图。

## 用 URL 传图

\`\`\`bash
curl {{BASE_URL}}/v1/chat/completions \\
  -H "Authorization: Bearer $WANCHUAN_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "MODEL_ID",
    "messages": [{
      "role": "user",
      "content": [
        {"type": "text", "text": "这张图里有哪些控件？"},
        {"type": "image_url",
         "image_url": {"url": "https://example.com/screenshot.png"}}
      ]
    }]
  }'
\`\`\`

URL 必须是公网可访问的直链。需要登录、带签名过期时间或只在内网可达的地址会拉取失败。若图片存放在私有存储，请改用 base64。

## 用 base64 传图

\`\`\`python
import base64
from openai import OpenAI

client = OpenAI(api_key="...", base_url="{{BASE_URL}}/v1")

with open("screenshot.png", "rb") as fp:
    encoded = base64.b64encode(fp.read()).decode()

resp = client.chat.completions.create(
    model="MODEL_ID",
    messages=[{
        "role": "user",
        "content": [
            {"type": "text", "text": "这张图里有哪些控件？"},
            {"type": "image_url", "image_url": {
                # 注意 data URI 前缀要和真实图片类型一致
                "url": f"data:image/png;base64,{encoded}",
            }},
        ],
    }],
)
print(resp.choices[0].message.content)
\`\`\`

base64 会让请求体膨胀约三分之一，大图容易撞上请求体大小限制（表现为 413，见 [错误码与排障](/help/guides/troubleshooting)）。本地图片、私有存储或一次性截图适合用它；重复使用的图片更适合先上传到可公开访问的位置再用 URL。

## 多图与图文混排

一条消息里可以放多张图，配合文字说明各图的角色。

\`\`\`json
"content": [
  {"type": "text", "text": "左边是改版前，右边是改版后，列出布局差异"},
  {"type": "image_url", "image_url": {"url": "https://example.com/before.png"}},
  {"type": "image_url", "image_url": {"url": "https://example.com/after.png"}}
]
\`\`\`

图片数量越多，占用的输入 token 越多。做对比任务时，两三张通常已足够；一次塞十几张既贵又容易让模型混淆。

## 成本与清晰度权衡

- 图片按视觉 token 计入**输入**侧费用，分辨率越高消耗越多。换算规则见 [计费与赠金说明](/help/guides/billing)。
- 截图类任务先裁掉无关区域，比整屏上传更省也更准。
- 需要识别小字（例如日志、报错弹窗）时不要过度压缩，压过头会导致模型读错字符，反而要多轮追问。
- 多轮对话里历史图片会持续占用上下文。长会话应在拿到结论后移除旧图片块。
`.trim(),
}

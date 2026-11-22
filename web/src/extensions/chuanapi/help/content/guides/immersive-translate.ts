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

export const immersiveTranslate: GuideDoc = {
  slug: 'immersive-translate',
  title: '接入沉浸式翻译',
  description: '让沉浸式翻译使用本站中转的模型做网页翻译。',
  updated: '2026-08-30',
  body: `
> 沉浸式翻译的自定义 AI 翻译服务走 OpenAI 兼容协议。注意它的「自定义 API 接口地址」通常要填**完整端点**（以 \`/chat/completions\` 结尾），不是 Base URL。

## 配置步骤

1. 在 [控制台 → API 令牌](/keys) 创建令牌。建议给翻译单独建一个令牌，方便在 [使用日志](/usage-logs) 里单独看翻译花费。
2. 打开沉浸式翻译的设置页，进入「翻译服务」。
3. 选择 OpenAI（或自定义 AI 翻译服务），填写：
   - **APIKEY**：控制台创建的令牌
   - **模型**：从 [定价页](/pricing) 复制真实 model id，翻译场景选便宜快速的模型即可
   - **自定义 API 接口地址**：\`{{BASE_URL}}/v1/chat/completions\`（部分版本只要求填到 \`{{BASE_URL}}/v1\`，以设置项旁的说明为准）
4. 保存后在任意外文页面测试一次翻译。

## 省钱建议

- 翻译是高频小请求，模型单价对总费用影响很大；优先选目录里输入单价低的模型。
- 「每秒最大请求数」调低一点可以避免撞 429 限流。
- 用独立令牌后，某天费用异常时能直接定位是翻译流量还是其他客户端，见 [控制台导览](/help/guides/console-tour)。

## 常见问题

- **翻译无响应 / 报 404**：接口地址填成了 Base URL 而没带 \`/v1/chat/completions\`，或多拼了一层 \`/v1\`。
- **401**：令牌拼错、带了空格，或误把 \`Bearer \` 写进了密钥值。
- **429**：请求频率过高，调低扩展的并发/频率设置。
- **翻译质量差**：换一个能力更强的模型，或在扩展的提示词设置里约束译文风格。

本页基于 2026-08-30 校订。沉浸式翻译为第三方扩展，设置项名称随版本变化，按「OpenAI 兼容 + 接口地址 + APIKEY + 模型」四要素对应即可。
`.trim(),
}

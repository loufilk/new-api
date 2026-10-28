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

export const clineRoo: GuideDoc = {
  slug: 'cline-roo',
  title: '接入 Cline / Roo Code',
  description:
    '在 VS Code 中配置 OpenAI Compatible 供应商，并确认工具调用可用。',
  updated: '2026-08-30',
  body: `
> 编码代理高度依赖**工具调用**。选模型时先确认它支持 tools，否则代理会一直卡在「无法编辑文件」。

两者都是 VS Code 里的编码代理，配置方式基本一致：选 OpenAI Compatible，填 Base URL 与 Key。关键在于选对支持工具调用的模型。

## 前置条件

- 已在 [控制台 → API 令牌](/keys) 创建令牌，账户余额充足。
- VS Code 已安装 Cline 或 Roo Code 扩展。

## 配置步骤

1. 打开扩展面板右上角的设置。
2. **API Provider** 选择 \`OpenAI Compatible\`。不要选 Anthropic 或 OpenAI 官方选项，那两个会走各自的固定域名。
3. **Base URL** 填 \`{{BASE_URL}}/v1\`。
4. **API Key** 填控制台创建的令牌。
5. **Model ID** 手动填入 [定价页](/pricing) 中的真实 id。这两个扩展通常不会自动拉取列表。
6. 如果设置里有 \`Function Calling\` 或 \`Tool Use\` 开关，保持开启。

## 验证工具调用

光验证「能对话」不够，编码代理真正依赖的是工具调用。下面这条请求带了一个空工具，能确认该模型在本站这边确实接受 tools 参数：

\`\`\`bash
curl {{BASE_URL}}/v1/chat/completions \\
  -H "Authorization: Bearer $WANCHUAN_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "MODEL_ID",
    "messages": [{"role": "user", "content": "你好"}],
    "tools": [{
      "type": "function",
      "function": {
        "name": "ping",
        "description": "connectivity probe",
        "parameters": {"type": "object", "properties": {}}
      }
    }]
  }'
\`\`\`

返回 200 说明链路通。若返回 400 且提示不支持 tools，就换一个支持工具调用的模型。

## 常见问题

- **代理不会改文件，只会聊天**：模型不支持工具调用，或扩展里的工具开关被关掉了。先按上面的 curl 验证。
- **404**：Base URL 漏了 \`/v1\`，或 model id 拼错。
- **429 频繁出现**：编码代理会在一次任务里连续发很多请求，容易撞限流。降低并发后重试。
- **费用比预期高**：这类代理每一步都会把完整上下文重发一遍。控制任务粒度、及时开新会话能显著降本，计费口径见 [计费与赠金说明](/help/guides/billing)。

## 相关文档

- [工具调用](/help/guides/function-calling)——tools / tool_choice 的完整语义。
- [接入 Claude Code](/help/guides/claude-code)——终端里的另一种编码代理方案。

本页基于 2026-08-30 的 Cline / Roo Code 版本校订。两个扩展的设置项名称接近但不完全相同，按「OpenAI Compatible + Base URL + Key + Model ID」四要素对应即可。
`.trim(),
}

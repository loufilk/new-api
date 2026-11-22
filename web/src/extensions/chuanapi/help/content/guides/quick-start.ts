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

export const quickStart: GuideDoc = {
  slug: 'quick-start',
  title: '快速开始：5 分钟发出第一个请求',
  description: '注册、创建令牌，并发出你的第一个请求。',
  recommended: true,
  updated: '2026-08-30',
  body: `
> Base URL 固定为 \`{{BASE_URL}}/v1\`。令牌只在控制台创建，请妥善保管，泄露后立即在控制台删除重建。

万川是一个 OpenAI 兼容的多模型 API 网关：你在这里创建一个 \`sk-\` 开头的令牌，然后像调用 OpenAI / Anthropic 官方接口一样调用它，站点会把请求转发到各家模型。

## 第 1 步：注册并登录

在右上角完成注册并登录。注册需要邮箱验证，收不到验证码时先检查垃圾邮件目录。

## 第 2 步：创建 API 令牌

进入 [控制台 → API 令牌](/keys)，点击「创建令牌」。建议：

- 每个应用 / 工具使用独立令牌，方便单独禁用与统计
- 新手直接使用默认分组即可；额度选「无限额度」时实际消耗钱包余额
- 令牌属于敏感凭证，请勿公开分享；泄露时在列表中删除并重建即可

## 第 3 步：确认余额

进入 [钱包](/wallet) 查看余额。余额不足时充值或使用兑换码；每次调用的花费会记录在 [使用日志](/usage-logs) 里。

## 第 4 步：发送第一个请求

\`\`\`bash
curl {{BASE_URL}}/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer $WANCHUAN_API_KEY" \\
  -d '{
    "model": "MODEL_ID",
    "messages": [{"role": "user", "content": "用一句话打个招呼"}]
  }'
\`\`\`

把 \`MODEL_ID\` 换成 [定价页](/pricing) 展示的真实 model id——不要沿用其他平台的模型名，目录之外的名称会返回 404。也可以用接口列出当前令牌可见的模型：

\`\`\`bash
curl {{BASE_URL}}/v1/models \\
  -H "Authorization: Bearer $WANCHUAN_API_KEY"
\`\`\`

## 第 5 步：验证与下一步

最快的验证方式是直接在站内对话：打开 [文本生成](/playground)，选择模型发一句话，能收到回复说明令牌与余额全部就绪。

## 首调排查清单

按这个顺序查，基本能覆盖第一次接入的全部失败：

1. **404**：Base URL 漏了 \`/v1\`，或 model id 不在目录里。
2. **401**：令牌缺失或拼错；检查是否把 \`Bearer\` 重复写进了令牌值。
3. **402**：余额不足，先到 [钱包](/wallet) 充值。
4. **403**：令牌有效但分组不含该模型，换一个目录中可用的 model id 验证。
5. **400**：参数越界，常见是 \`max_tokens\` 超过该模型的最大输出。

## 接下来按你的工具选择指南

- 编程工具：[接入 Claude Code](/help/guides/claude-code)、[接入 Codex CLI](/help/guides/codex-cli)，或用 [CC Switch 一键导入](/help/guides/cc-switch)
- 桌面客户端：[接入 Cherry Studio](/help/guides/cherry-studio)
- 浏览器翻译：[接入沉浸式翻译](/help/guides/immersive-translate)
- 写代码：[接入 OpenAI SDK](/help/guides/openai-sdk)、[接入 Anthropic SDK](/help/guides/anthropic-sdk)
- 遇到报错：[错误码与排障](/help/guides/troubleshooting)
`.trim(),
}

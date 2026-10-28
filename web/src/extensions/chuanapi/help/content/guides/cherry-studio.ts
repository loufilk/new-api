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

export const cherryStudio: GuideDoc = {
  slug: 'cherry-studio',
  title: '接入 Cherry Studio',
  description: '在 Cherry Studio 中把本站添加为 OpenAI 类型服务商。',
  updated: '2026-08-30',
  body: `
> 供应商类型要选 **OpenAI 兼容**，API 地址填 \`{{BASE_URL}}/v1\`，末尾的 \`/v1\` 不能省。

Cherry Studio 支持任意 OpenAI 兼容供应商。新增一个供应商、填两个字段就能用上本站的全部目录模型。

## 前置条件

- 已在 [控制台 → API 令牌](/keys) 创建令牌，账户余额充足。
- 本机已安装 Cherry Studio 桌面端。

## 配置步骤

1. 打开设置，进入模型服务（或称模型提供商）页面。
2. 新增一个供应商，类型选择 **OpenAI** 或 **OpenAI 兼容**，名称可以填「万川」。
3. API 地址填 \`{{BASE_URL}}/v1\`。部分版本会把地址拆成「主机」与「路径」两栏，此时确保最终拼出来的仍然以 \`/v1\` 结尾。
4. API 密钥填控制台创建的令牌。
5. 点击「获取模型」自动拉取目录；如果该版本不支持自动拉取，就从 [定价页](/pricing) 复制真实 model id 手动添加。
6. 保存后新建一次对话验证。

## 手动确认可用模型

如果客户端拉取列表失败，可以先在终端里确认当前密钥能看到哪些模型，再把 id 抄进客户端：

\`\`\`bash
curl {{BASE_URL}}/v1/models \\
  -H "Authorization: Bearer $WANCHUAN_API_KEY"
\`\`\`

## 常见问题

- **404 / 模型列表为空**：API 地址漏了 \`/v1\`，或者客户端自动补了一层导致重复。用上面的 curl 对照一下就能确认是哪一种。
- **401**：密钥填错或前后带了空格。重新从控制台复制一次。
- **403**：密钥有效但令牌分组不含该模型，换一个目录里可用的。
- **对话能开始但中途断流**：先看是不是触发了限流（429），再检查客户端的超时设置。相关行为见 [流式输出](/help/guides/streaming)。

## 相关文档

- [视觉输入](/help/guides/vision)——在客户端里发图片前先确认模型支持。
- [计费与赠金说明](/help/guides/billing)——桌面客户端的长上下文会显著影响费用。

本页基于 2026-08-30 的 Cherry Studio 版本校订。该客户端设置项名称随版本变动，如界面与本文描述不一致，按「OpenAI 兼容供应商 + Base URL + Key」三要素对应即可。
`.trim(),
}

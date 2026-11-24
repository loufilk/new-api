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

export const playground: GuideDoc = {
  slug: 'playground',
  title: '文本生成在线对话',
  description: '用登录会话在站内试聊和对比模型；API 密钥需另外验证。',
  updated: '2026-11-23',
  body: `
[文本生成](/playground) 是站内的在线对话页：登录后选择模型直接发消息，不需要写任何代码。它有两个典型用途。

## 用途一：试聊并验证当前会话

想分别验证网页对话和客户端接入，可以这样做：

1. 在 [文本生成](/playground) 选择分组和模型，发一句话。
2. 网页回复说明本次登录会话在所选分组成功调用了该模型，没有验证客户端的 API 密钥。
3. 要验证客户端，请按 [快速开始](/help/guides/quick-start) 用它实际配置的密钥查询模型，再发送一次目标协议的请求。

## 用途二：对比模型

同一个问题在不同模型间切换重发，直观对比回答质量与速度，再决定客户端里配哪个 model id。模型单价见 [定价页](/pricing)。

## 注意事项

- 文本生成对话与 API 调用一样按 token 计费，费用记入 [使用日志](/usage-logs)。
- 网页模型列表取决于登录会话和所选分组，不一定与客户端用自己的密钥查询 \`/v1/models\` 的结果相同。
- 长对话的历史会随每次发送重复计入输入费用，测试完记得开新会话。
`.trim(),
}

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
  description: '不写代码，在站内直接选模型对话，验证令牌与余额是否就绪。',
  updated: '2026-08-30',
  body: `
[文本生成](/playground) 是站内的在线对话页：登录后选择模型直接发消息，不需要写任何代码。它有两个典型用途。

## 用途一：验证接入是否就绪

第一次接入时，最快的自检顺序是：

1. 在 [文本生成](/playground) 选一个模型发一句话。
2. 能收到回复——账号、余额、模型可用性全部就绪，接下来放心配置客户端；配置后再报错就是客户端自身的问题。
3. 收不到回复——先看报错内容，对照 [错误码与排障](/help/guides/troubleshooting) 处理，不用先折腾客户端。

## 用途二：对比模型

同一个问题在不同模型间切换重发，直观对比回答质量与速度，再决定客户端里配哪个 model id。模型单价见 [定价页](/pricing)。

## 注意事项

- 文本生成对话与 API 调用一样按 token 计费，费用记入 [使用日志](/usage-logs)。
- 模型列表来自你账号可见的目录，与 \`/v1/models\` 返回一致；看不到某个模型说明令牌分组不含它。
- 长对话的历史会随每次发送重复计入输入费用，测试完记得开新会话。
`.trim(),
}

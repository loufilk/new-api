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

export const billing: GuideDoc = {
  slug: 'billing',
  title: '计费与赠金说明',
  description: '预付费钱包、单价口径、赠金与邀请奖励规则与账单核对步骤。',
  updated: '2026-08-30',
  body: `
> 最终扣费以 [使用日志](/usage-logs) 与 [钱包](/wallet) 账单为准；[定价页](/pricing) 是单价口径的权威来源。

## 预付费钱包

- 先在 [钱包](/wallet) 充值，再按实际调用扣减余额，没有月席位费。
- 余额不足时计费调用返回 402，充值后即可恢复。
- 支付渠道以钱包页当前启用项为准；兑换码同样在钱包页使用。

## 余额的三个部分

- **充值余额**：实际付款获得，可用于全部公开目录模型。
- **注册赠金**：仅可用于标注了可用赠金的模型。
- **邀请奖励**：好友前三次成功在线余额充值各返充值额度的 5%，先记入钱包页的待使用收益；划转到余额后可在全部模型使用。消费时优先扣注册赠金（如模型支持），再扣邀请奖励，最后走充值余额。

邀请奖励按规则比例计算并向下取整到最小额度；兑换码、管理员加额、外部订阅与余额购买订阅不计入返佣次数。

## 单价怎么看

[定价页](/pricing) 直接展示每个按量模型的**输入单价与输出单价（每 100 万 tokens）**，不需要自己做倍率换算。两点提醒：

- 输出通常比输入贵，长回答的成本大头在输出侧。
- 部分模型的输入侧区分缓存写入与缓存读取，两者单价不同，命中缓存能明显降低成本。

## 按次计费

部分模型（常见于图像生成）按调用次数计价而不是按 token，定价页会直接展示每次调用的固定单价。

## 估算一次调用的成本

先确认输入与输出的 token 量级，再套用定价页单价。几项容易被漏掉的输入成本：

- 多轮对话每次都会把完整历史作为输入重新计费，历史越长越贵。
- 工具调用的结果会作为消息进入下一轮输入，见 [工具调用](/help/guides/function-calling)。
- 图片按视觉 token 计入输入侧，分辨率越高越贵，见 [视觉输入](/help/guides/vision)。
- 流式响应的增量通常不携带完整用量字段，不要依赖它记账。

## 核对账单

1. 在 [定价页](/pricing) 确认 model id 与当前单价。
2. 在 [使用日志](/usage-logs) 中按时间与 model id 定位具体请求，核对输入 / 输出 token 数与扣费。
3. 想弄清「哪个客户端花的」，按令牌筛选——前提是你按用途分开建了令牌，见 [控制台导览](/help/guides/console-tour)。
`.trim(),
}

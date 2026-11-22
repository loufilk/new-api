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

export const consoleTour: GuideDoc = {
  slug: 'console-tour',
  title: '控制台导览：令牌、钱包与日志',
  description: '按场景速览控制台功能：花费、用量观测与账号安全。',
  updated: '2026-08-30',
  body: `
按你实际会用到的顺序过一遍控制台。重点是最后那条账单核对动线——它能把「这笔钱花在哪」定位到具体请求。

## 数据看板

[数据看板](/dashboard) 给的是账户当前状态的快照：余额、近期消耗趋势、调用量。日常只需要扫一眼余额和趋势有没有突变——趋势突然抬高通常意味着某个客户端在重试或者上下文变长了。

## API 令牌

[令牌页](/keys) 负责创建、查看和删除令牌。几条实践建议：

- **按用途分开建令牌**：编码代理一个、生产服务一个、临时测试一个。这样任何一个泄漏或超支时可以单独禁用，不影响其他。
- **配合使用日志定位**：日志里能按令牌过滤，分开建令牌后才能看清各用途分别花了多少。
- **令牌只在这里创建和删除**，泄露时删除重建即可，站点不会通过其他渠道向你索要令牌。

## 钱包与余额

[钱包页](/wallet) 展示余额构成、充值入口与账单历史。万川的余额分三部分：

- **充值余额**：实际付款获得，可用于全部公开目录模型。
- **注册赠金**：仅可用于标注了可用赠金的模型。
- **邀请奖励**：好友前三次成功在线余额充值各返充值额度的 5%，先记入钱包页的待使用收益；划转到余额后可在全部模型使用。消费时优先扣注册赠金（如模型支持），再扣邀请奖励，最后走充值余额。

邀请奖励的比例来自系统有效规则；兑换码、管理员加额、外部订阅与余额购买订阅不计入返佣次数。兑换码在钱包页输入使用。

## 使用日志

[使用日志](/usage-logs) 是排查费用问题唯一可靠的地方。每条记录包含时间、模型、输入与输出 token 数、命中的倍率和最终扣费。

注意区分两组数字：**输入 token** 里可能包含缓存写入与缓存读取，两者单价不同；**输出 token** 通常有独立的输出倍率。具体换算见 [计费与赠金说明](/help/guides/billing)。

## 账单核对动线

发现某天花超了，按这个顺序查最快：

1. 钱包页看当天扣费总额，确认是真的超了还是感知偏差。
2. 使用日志按当天筛选，先按**模型**分组看是哪个模型占大头。
3. 再按**令牌**筛，定位到具体是哪个客户端或服务。这一步能否奏效，取决于你有没有按用途分开建令牌。
4. 展开那几条最贵的记录，看是输入侧还是输出侧膨胀。输入侧膨胀通常是上下文没截断或缓存没命中；输出侧膨胀通常是 \`max_tokens\` 给太大。
5. 如果是编码代理导致的，回到对应的客户端指南调整上下文策略，例如 [接入 Cline / Roo Code](/help/guides/cline-roo)。

## 个人设置

[个人设置](/profile) 管理邮箱绑定、密码、两步验证与 Passkey。建议至少绑定邮箱并开启两步验证——余额是真金白银，账号被盗的代价是直接的。

## 相关文档

- [计费与赠金说明](/help/guides/billing)——单价与赠金规则如何变成账单金额。
- [认证与密钥](/help/guides/authentication)——密钥格式与 401 / 403 的区分。
`.trim(),
}

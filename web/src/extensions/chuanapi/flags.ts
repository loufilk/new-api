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
/**
 * ChuanAPI 客户端可见性开关。
 *
 * 这些常量存在的唯一目的，是让上游代码保持原样、只在外层加一层条件，
 * 从而把「删除上游代码」式的定制改成「门控」式定制，显著降低跨版本
 * 升级时的补丁冲突面。
 *
 * 它们是编译期常量，不是运行时选项，也不接后台配置——AGENTS.md 规定
 * 客户端不得暴露 affiliate 额度、提现、手动成熟或转账入口，因此不提供
 * 任何可以在生产被误开的开关。构建时会被摇树消除。
 */

/**
 * 是否展示上游的 affiliate 额度字段（邀请收益、邀请额度）。
 *
 * 恒为 false：管理端用户列表/用量详情不展示 aff_quota 原始字段；用户本人
 * 的待使用收益由钱包页「邀请奖励」卡片按原生 aff_quota/aff_history 展示，
 * 并通过划转进入 reward 余额。此处不提供任何生产可误开的开关。
 */
export const SHOW_LEGACY_AFFILIATE_QUOTA = false as const

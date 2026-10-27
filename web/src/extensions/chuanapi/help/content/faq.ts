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
import type { FAQItem } from '@/features/dashboard/types'

/**
 * 静态兜底 FAQ：当管理后台未配置（/api/status 的 faq 为空或关闭）时展示。
 * 管理员在 控制台 → 系统设置 → 仪表盘 配置 FAQ 后自动替换为动态内容。
 */
export const fallbackFaq: readonly FAQItem[] = [
  {
    question: 'Base URL 应该填什么？',
    answer:
      'OpenAI 兼容客户端填 `{{BASE_URL}}/v1`；Anthropic 协议客户端（如 Claude Code）填 `{{BASE_URL}}`，不带 `/v1`。分不清时看 [错误码与排障](/help/guides/troubleshooting)。',
  },
  {
    question: '模型名应该填什么？',
    answer:
      '以 [定价页](/pricing) 公开目录中的真实 model id 为准，不要沿用其他平台的模型名；目录之外的名称会返回 404。',
  },
  {
    question: '为什么返回 401 / 403？',
    answer:
      '401 是认证没通过：令牌缺失、拼错或已删除。403 是认证通过但没有权限：通常是令牌分组不含目标模型。详见 [认证与密钥](/help/guides/authentication)。',
  },
  {
    question: '余额和计费怎么看？',
    answer:
      '余额在 [钱包](/wallet)，逐条调用明细在 [使用日志](/usage-logs)；计价规则见 [计费与赠金说明](/help/guides/billing)。',
  },
]

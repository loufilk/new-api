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
import { formatMinorMoney } from '@/features/agent-platform/lib/money'
import { formatTimestamp } from '@/lib/format'

import { formatInvoiceRate, type InvoiceDetail } from '../contracts'
import type { AdminDetail } from './api'

type FeeState = NonNullable<InvoiceDetail['fee_state']>
const states: Record<FeeState, readonly [string, string]> = {
  none: ['No fee required', '无需手续费'],
  unpaid: ['Unpaid', '未支付'],
  checkout: ['Payment in progress', '支付处理中'],
  paid: ['Paid', '已支付'],
  refund_review: ['Fee refund review', '手续费退款待人工核对'],
  refunded: ['Refunded', '已退款'],
}

export function financeFeeCleared(inv: InvoiceDetail): boolean {
  if (!inv.fee_minor || inv.fee_minor === '0') {
    // Old applications predate the fee fields and have no fee to collect.
    if (!inv.fee_minor && (inv.settings_version ?? 0) > 0) return false
    return (
      (!inv.fee_state || inv.fee_state === 'none') &&
      !inv.fee_rail &&
      (!inv.fee_paid_minor || inv.fee_paid_minor === '0')
    )
  }
  return (
    /^[1-9]\d*$/.test(inv.fee_minor) &&
    inv.fee_state === 'paid' &&
    (inv.fee_rail === 'epay' || inv.fee_rail === 'wallet') &&
    inv.fee_paid_minor === inv.fee_minor &&
    (inv.fee_paid_at ?? 0) > 0
  )
}

export function feeFacts(
  inv: AdminDetail,
  label: (en: string, zh: string) => string,
  locale?: string
) {
  if ((inv.settings_version ?? 0) <= 0) return []
  const rail =
    inv.fee_rail === 'wallet'
      ? label('Paid wallet balance', '钱包付费余额')
      : null
  const method =
    inv.fee_rail === 'epay'
      ? [label('Epay', '易支付'), inv.fee_payment_method]
          .filter(Boolean)
          .join(' · ')
      : rail
  const state = inv.fee_state && states[inv.fee_state]
  return [
    [
      label('Fee at application', '申请时手续费'),
      inv.fee_minor != null
        ? formatMinorMoney(inv.fee_minor, inv.currency, 2, locale)
        : null,
    ],
    [
      label('Fee rate at application', '申请时手续费率'),
      inv.fee_rate_bps != null
        ? formatInvoiceRate(inv.fee_rate_bps, locale)
        : null,
    ],
    [
      label('Configured tax rate at application', '申请时配置税率'),
      inv.tax_rate_bps != null
        ? formatInvoiceRate(inv.tax_rate_bps, locale)
        : null,
    ],
    [
      label('Fee payment status', '手续费支付状态'),
      state ? label(...state) : null,
    ],
    [label('Fee payment method', '手续费支付方式'), method],
    [label('Fee merchant order', '手续费商户订单号'), inv.fee_trade_no],
    [
      label('Fee provider reference', '手续费支付平台流水号'),
      inv.fee_provider_reference,
    ],
    [
      label('Paid invoice fee', '实付手续费'),
      inv.fee_paid_minor != null && inv.fee_minor !== '0'
        ? formatMinorMoney(inv.fee_paid_minor, inv.currency, 2, locale)
        : null,
    ],
    [
      label('Fee paid at', '手续费支付时间'),
      inv.fee_paid_at ? formatTimestamp(inv.fee_paid_at) : null,
    ],
  ]
}

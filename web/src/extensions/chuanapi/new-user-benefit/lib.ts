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
import { toIntlLocale } from '@/i18n/languages'
import {
  getServerErrorMessage,
  getServerErrorSources,
} from '@/lib/server-error-message'

import type { BenefitOffer, NewUserBenefitInfo } from './types'

export const BENEFIT_QUERY_KEY = ['new-user-benefit'] as const
export const BENEFIT_CONFIG_QUERY_KEY = ['new-user-benefit-config'] as const

/** Parse cents without rounding, including for the administrator's input. */
export function parseCnyCents(value: string): number | null {
  if (!/^\d{1,14}(?:\.\d{1,2})?$/.test(value)) return null
  const [whole, fraction = ''] = value.split('.')
  const cents = Number(whole) * 100 + Number(fraction.padEnd(2, '0'))
  return Number.isSafeInteger(cents) && cents > 0 && cents <= 100_000_000
    ? cents
    : null
}

export function normalizeCnyAmount(value: string): string {
  const cents = parseCnyCents(value)
  if (cents === null) return value
  return `${Math.floor(cents / 100)}.${String(cents % 100).padStart(2, '0')}`
}

export function formatBenefitCny(value: string, language: string): string {
  const cents = parseCnyCents(value)
  if (cents === null) return '-'
  return new Intl.NumberFormat(toIntlLocale(language), {
    style: 'currency',
    currency: 'CNY',
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(cents / 100)
}

export function getBenefitOffer(
  info: NewUserBenefitInfo | undefined
): BenefitOffer | null {
  if (!info) return null
  let offer: BenefitOffer
  if (info.status === 'pending' && info.pending_order) {
    const order = info.pending_order
    offer = {
      payAmount: order.pay_amount_cny,
      creditAmount: order.credit_amount_cny,
      version: order.config_version,
      orderId: order.order_id,
      methods: info.pay_methods.filter(
        (method) => method.type === order.payment_method
      ),
    }
  } else if (info.status === 'available' && info.enabled && info.eligible) {
    offer = {
      payAmount: info.pay_amount_cny,
      creditAmount: info.credit_amount_cny,
      version: info.config_version,
      methods: info.pay_methods,
    }
  } else {
    return null
  }

  const pay = parseCnyCents(offer.payAmount)
  const credit = parseCnyCents(offer.creditAmount)
  if (
    pay === null ||
    credit === null ||
    credit < pay ||
    !Number.isSafeInteger(offer.version) ||
    offer.version < 0
  ) {
    return null
  }
  return offer
}

export function isBenefitConfigConflict(error: unknown): boolean {
  return getServerErrorSources(error).some(
    (source) => source.code === 'new_user_benefit_config_changed'
  )
}

export function benefitErrorMessage(error: unknown, fallback: string): string {
  const messages: Record<string, string> = {
    new_user_benefit_unavailable:
      'New user benefit is temporarily unavailable.',
    new_user_benefit_ineligible:
      'This account is not eligible for this recharge benefit.',
    new_user_benefit_payment_method_unavailable:
      'This benefit payment method is currently unavailable. Please try again later.',
    new_user_benefit_invalid_config:
      'Enter valid CNY amounts with at most two decimal places. Total credit must be at least the payment amount.',
  }
  for (const source of getServerErrorSources(error)) {
    if (typeof source.code === 'string' && messages[source.code]) {
      return messages[source.code]
    }
  }
  return getServerErrorMessage(error, fallback)
}

export function createBenefitIdempotencyKey(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = Array.from(bytes, (value) =>
    value.toString(16).padStart(2, '0')
  ).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

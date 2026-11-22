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
import type { TFunction } from 'i18next'
import { z } from 'zod'

import { formatNumber } from '@/lib/format'

import { parseCnyCents } from '../lib'

export function createBenefitConfigSchema(
  t: TFunction,
  locale: string | undefined,
  turnstileReady: boolean
) {
  const amount = z
    .string()
    .refine(
      (value) => parseCnyCents(value) !== null,
      t(
        'Enter a CNY amount from 0.01 to 1,000,000.00 with at most two decimal places.'
      )
    )
  const orderLimit = z.string().refine(
    (value) => /^[1-9]\d*$/.test(value) && Number(value) <= 1_000_000,
    t('Enter a whole number from 1 to {{max}}.', {
      max: formatNumber(1_000_000, locale),
    })
  )
  return z
    .object({
      enabled: z.boolean(),
      require_verified_email: z.boolean(),
      require_turnstile: z.boolean(),
      network_limit_enabled: z.boolean(),
      payment_identity_check_enabled: z.boolean(),
      pay_amount_cny: amount,
      credit_amount_cny: amount,
      max_total_orders: orderLimit,
      max_orders_per_24h: orderLimit,
      max_orders_per_network_24h: z.string(),
      total_bonus_budget_cny: amount,
    })
    .superRefine((values, context) => {
      const pay = parseCnyCents(values.pay_amount_cny)
      const credit = parseCnyCents(values.credit_amount_cny)
      if (pay !== null && credit !== null && credit < pay) {
        context.addIssue({
          code: 'custom',
          path: ['credit_amount_cny'],
          message: t('Total credit must be at least the payment amount.'),
        })
      }
      if (
        values.network_limit_enabled &&
        (!/^[1-9]\d*$/.test(values.max_orders_per_network_24h) ||
          Number(values.max_orders_per_network_24h) > 10_000)
      ) {
        context.addIssue({
          code: 'custom',
          path: ['max_orders_per_network_24h'],
          message: t('Enter a whole number from 1 to {{max}}.', {
            max: formatNumber(10_000, locale),
          }),
        })
      }
      if (values.enabled && values.require_turnstile && !turnstileReady) {
        context.addIssue({
          code: 'custom',
          path: ['require_turnstile'],
          message: t(
            'Configure and enable Turnstile before enabling the new user benefit'
          ),
        })
      }
    })
}

export type BenefitConfigValues = z.infer<
  ReturnType<typeof createBenefitConfigSchema>
>

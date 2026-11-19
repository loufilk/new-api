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
import { z } from 'zod'

import { minorMoneyInput } from '@/features/agent-platform/lib/money'

import type { InvoiceSettings, InvoiceTypeSettings } from '../contracts'

// Preserve the exact decimal entered; never round a financial setting.
export function parseRate(text: string): number | null {
  if (!/^\d{1,3}(?:\.\d{1,2})?$/.test(text)) return null
  const [whole, fraction = ''] = text.split('.')
  const bps = Number(whole) * 100 + Number(fraction.padEnd(2, '0'))
  return bps <= 10000 ? bps : null
}

export function settingsFormSchema(rateError: string) {
  const rate = z.string().refine((text) => parseRate(text) !== null, rateError)
  const policy = z.object({
    enabled: z.boolean(),
    fee_enabled: z.boolean(),
    fee: rate,
    tax: rate,
  })
  return z.object({
    version: z.number().int(),
    ordinary: policy,
    special: policy,
  })
}
export type SettingsFormValues = z.infer<ReturnType<typeof settingsFormSchema>>

export function editSettings(settings: InvoiceSettings): SettingsFormValues {
  const toFields = (policy: InvoiceTypeSettings) => ({
    enabled: policy.enabled,
    fee_enabled: policy.fee_enabled,
    fee: minorMoneyInput(String(policy.fee_rate_bps), 2),
    tax: minorMoneyInput(String(policy.tax_rate_bps), 2),
  })
  return {
    version: settings.version,
    ordinary: toFields(settings.ordinary),
    special: toFields(settings.special),
  }
}

export function settingsPayload(
  draft: SettingsFormValues
): InvoiceSettings | null {
  function toPolicy(
    fields: SettingsFormValues['ordinary']
  ): InvoiceTypeSettings | null {
    const fee = parseRate(fields.fee)
    const tax = parseRate(fields.tax)
    if (fee === null || tax === null) return null
    return {
      enabled: fields.enabled,
      fee_enabled: fields.fee_enabled,
      fee_rate_bps: fee,
      tax_rate_bps: tax,
    }
  }
  const ordinary = toPolicy(draft.ordinary)
  const special = toPolicy(draft.special)
  return ordinary && special
    ? { version: draft.version, ordinary, special }
    : null
}

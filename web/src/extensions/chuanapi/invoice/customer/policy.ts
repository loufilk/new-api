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

import { invoiceSettingsSchema, type InvoiceTypeSettings } from '../contracts'

const paymentMethod = z.discriminatedUnion('rail', [
  z.object({
    rail: z.literal('wallet'),
    id: z.literal('paid_wallet'),
    name: z.string(),
  }),
  z.object({
    rail: z.literal('epay'),
    id: z.string().regex(/^[a-zA-Z0-9_-]{1,50}$/),
    name: z.string(),
  }),
])
export type FeePaymentMethod = z.infer<typeof paymentMethod>
const capabilities = z.object({
  enabled: z.boolean(),
  fee_payment_methods: z.array(paymentMethod).default([]),
  wallet_quota_per_cent: z.int().nonnegative().default(0),
})
const config = invoiceSettingsSchema.extend(capabilities.shape)
const legacyConfig = capabilities
  .extend({
    // Never downgrade an incomplete modern policy into two free invoice types.
    version: z.never().optional(),
    ordinary: z.never().optional(),
    special: z.never().optional(),
  })
  .transform((base) => {
    const free: InvoiceTypeSettings = {
      enabled: base.enabled,
      fee_enabled: false,
      fee_rate_bps: 0,
      tax_rate_bps: 0,
    }
    return { ...base, version: undefined, ordinary: free, special: free }
  })

export const customerConfigSchema = z.union([config, legacyConfig])
export type CustomerInvoiceConfig = z.infer<typeof customerConfigSchema>

/** Sum the selected order values before rounding the fee once to the nearest fen. */
export function estimateInvoiceFee(
  amountMinor: bigint,
  policy: InvoiceTypeSettings
): bigint {
  if (!policy.fee_enabled) return 0n
  return (amountMinor * BigInt(policy.fee_rate_bps) + 5_000n) / 10_000n
}

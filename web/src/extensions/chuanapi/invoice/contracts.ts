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

import { api } from '@/lib/api'
import { createServerError } from '@/lib/server-error-message'

export type InvoiceStatus =
  | 'awaiting_payment'
  | 'pending'
  | 'issued'
  | 'rejected'
  | 'cancelled'

const rate = z.int().min(0).max(10_000)
const typeSettings = z.object({
  enabled: z.boolean(),
  fee_enabled: z.boolean(),
  fee_rate_bps: rate,
  tax_rate_bps: rate,
})
export const invoiceSettingsSchema = z.object({
  version: z.int().nonnegative(),
  ordinary: typeSettings,
  special: typeSettings,
})
export type InvoiceSettings = z.infer<typeof invoiceSettingsSchema>
export type InvoiceTypeSettings = z.infer<typeof typeSettings>

export function formatInvoiceRate(bps: number, locale?: string): string {
  if (!Number.isInteger(bps) || bps < 0 || bps > 10_000) return '—'
  return Intl.NumberFormat(locale, {
    style: 'percent',
    maximumFractionDigits: 2,
  }).format(bps / 10_000)
}

export interface InvoiceOrder {
  id: number
  type: 'topup'
  reference: string
  paid_at: number
  amount_minor: string
  currency: 'CNY'
}

export interface InvoiceSummary {
  settings_version?: number
  fee_minor?: string
  fee_rate_bps?: number
  tax_rate_bps?: number
  fee_state?:
    | 'none'
    | 'unpaid'
    | 'checkout'
    | 'paid'
    | 'refunded'
    | 'refund_review'
  fee_rail?: '' | 'wallet' | 'epay'
  fee_payment_method?: string
  fee_paid_minor?: string
  fee_paid_at?: number
  wallet_fee_quota?: string
  wallet_quota_per_cent?: number
  id: number
  status: InvoiceStatus
  amount_minor: string
  currency: 'CNY'
  order_count: number
  invoice_type: 'ordinary' | 'special'
  title_type: 'personal' | 'company'
  category: 'technical_service' | 'software_development'
  created_at: number
  updated_at: number
  issued_at?: number
  processing?: boolean
  has_document: boolean
  user_id?: number
}

export interface InvoiceDetail extends InvoiceSummary {
  title: string
  tax_id?: string
  email: string
  address?: string
  phone?: string
  bank_name?: string
  bank_account?: string
  reason?: string
  invoice_number?: string
  orders: InvoiceOrder[]
}

export interface InvoicePage<T> {
  items: T[]
  total: number
}

/** Never open an external URL or render active PDF content in the console. */
export async function downloadInvoiceDocument(
  id: number,
  admin: boolean,
  signal?: AbortSignal
): Promise<void> {
  if (!Number.isSafeInteger(id) || id <= 0) throw createServerError(null)
  const path = admin ? '/api/admin/invoice' : '/api/invoice'
  const response = await api.get<Blob>(`${path}/applications/${id}/document`, {
    responseType: 'blob',
    disableDuplicate: true,
    signal,
  })
  const blob = response.data
  if (
    !(blob instanceof Blob) ||
    blob.size === 0 ||
    blob.type.split(';')[0] !== 'application/pdf'
  ) {
    throw createServerError(null, 'Invalid invoice document')
  }
  signal?.throwIfAborted()
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  try {
    link.href = url
    link.download = `invoice-${id}.pdf`
    document.body.appendChild(link)
    link.click()
  } finally {
    link.remove()
    // A download may resolve the blob asynchronously after the click.
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
}

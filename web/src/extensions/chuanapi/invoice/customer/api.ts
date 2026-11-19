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
import { api } from '@/lib/api'
import { createServerError } from '@/lib/server-error-message'

import type {
  InvoiceDetail,
  InvoiceOrder,
  InvoicePage,
  InvoiceStatus,
  InvoiceSummary,
} from '../contracts'
import type { InvoiceFields } from './form-schema'
import { customerConfigSchema } from './policy'

export const PAGE_SIZE = 20
export const ORDER_LIMIT = 100
export const invoiceKey = (owner: number) =>
  ['customer-invoice', owner] as const
export const privateRead = {
  gcTime: 0,
  staleTime: 0,
  retry: false,
  meta: { redirectOnInternalServerError: false },
} as const
export type InvoiceTotals = {
  available_minor: string
  pending_minor: string
  issued_minor: string
  available_count: number
  pending_count: number
  issued_count: number
  currency: 'CNY'
}

interface Envelope<T> {
  success: boolean
  message: string
  data: T
}
const base = '/api/invoice'
function unwrap<T>(envelope: Envelope<T>): T {
  if (envelope?.success !== true || envelope.data == null) {
    throw createServerError(envelope)
  }
  return envelope.data
}
async function read<T>(
  path: string,
  signal: AbortSignal,
  params?: Record<string, string | number>
): Promise<T> {
  const response = await api.get<Envelope<T>>(base + path, {
    signal,
    params,
    disableDuplicate: true,
  })
  return unwrap(response.data)
}

export const invoiceAPI = {
  config: async (signal: AbortSignal) => {
    const config = customerConfigSchema.safeParse(
      await read<unknown>('/config', signal)
    )
    if (!config.success) {
      throw createServerError(null, 'Unable to load invoice information')
    }
    return config.data
  },
  totals: (signal: AbortSignal) => read<InvoiceTotals>('/summary', signal),
  orders: (p: number, signal: AbortSignal) =>
    read<InvoicePage<InvoiceOrder>>('/orders', signal, { p, size: PAGE_SIZE }),
  history: (p: number, status: InvoiceStatus | '', signal: AbortSignal) =>
    read<InvoicePage<InvoiceSummary>>('/applications', signal, {
      p,
      size: PAGE_SIZE,
      status,
    }),
  detail: async (id: number, signal: AbortSignal) => {
    const detail = await read<InvoiceDetail>(`/applications/${id}`, signal)
    if (detail.id !== id) throw createServerError(null)
    return detail
  },
  async submit(
    fields: InvoiceFields,
    orderIds: number[],
    idempotencyKey: string,
    settingsVersion: number | undefined
  ) {
    const response = await api.post<Envelope<InvoiceSummary>>(
      `${base}/applications`,
      {
        ...fields,
        order_ids: orderIds,
        idempotency_key: idempotencyKey,
        ...(settingsVersion !== undefined && {
          settings_version: settingsVersion,
        }),
      }
    )
    return unwrap(response.data)
  },
  async walletFee(
    id: number,
    fee: string,
    quota: string,
    unitsPerCent: number
  ) {
    const response = await api.post<Envelope<InvoiceDetail>>(
      `${base}/applications/${id}/fee/wallet`,
      {
        fee_minor: fee,
        wallet_quota: quota,
        wallet_quota_per_cent: unitsPerCent,
      },
      { singleUseAuthorization: true }
    )
    const detail = unwrap(response.data)
    if (
      detail.id !== id ||
      detail.fee_state !== 'paid' ||
      detail.fee_paid_minor !== fee ||
      detail.fee_rail !== 'wallet'
    ) {
      throw createServerError(
        null,
        'Check the payment status before trying again.'
      )
    }
  },
  async epayFee(id: number, fee: string, method: string) {
    const response = await api.post<Envelope<unknown>>(
      `${base}/applications/${id}/fee/epay`,
      { payment_method: method },
      { singleUseAuthorization: true }
    )
    const parsed = z
      .object({
        url: z.url({ protocol: /^https$/ }),
        params: z.record(z.string(), z.string()),
      })
      .safeParse(unwrap(response.data))
    if (parsed.success) {
      const { url, params } = parsed.data
      const target = new URL(url)
      if (
        !target.username &&
        !target.password &&
        params.money === minorMoneyInput(fee, 2) &&
        params.type === method
      ) {
        return { url, params }
      }
    }
    throw createServerError(
      null,
      'Check the payment status before trying again.'
    )
  },
  async cancel(id: number) {
    const response = await api.post<Envelope<InvoiceSummary>>(
      `${base}/applications/${id}/cancel`
    )
    return unwrap(response.data).id
  },
}

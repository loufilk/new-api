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
  config: (signal: AbortSignal) =>
    read<{ enabled: boolean }>('/config', signal),
  totals: (signal: AbortSignal) => read<InvoiceTotals>('/summary', signal),
  orders: (p: number, signal: AbortSignal) =>
    read<InvoicePage<InvoiceOrder>>('/orders', signal, { p, size: PAGE_SIZE }),
  history: (p: number, status: InvoiceStatus | '', signal: AbortSignal) =>
    read<InvoicePage<InvoiceSummary>>('/applications', signal, {
      p,
      size: PAGE_SIZE,
      status,
    }),
  detail: (id: number, signal: AbortSignal) =>
    read<InvoiceDetail>(`/applications/${id}`, signal),
  async submit(
    fields: InvoiceFields,
    orderIds: number[],
    idempotencyKey: string
  ) {
    const response = await api.post<Envelope<InvoiceSummary>>(
      `${base}/applications`,
      {
        ...fields,
        order_ids: orderIds,
        idempotency_key: idempotencyKey,
      }
    )
    return unwrap(response.data).id
  },
  async cancel(id: number) {
    const response = await api.post<Envelope<InvoiceSummary>>(
      `${base}/applications/${id}/cancel`
    )
    return unwrap(response.data).id
  },
}

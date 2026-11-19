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
  InvoicePage,
  InvoiceStatus,
  InvoiceSummary,
} from '../contracts'

export type ReviewFilter = InvoiceStatus | 'refund_review' | ''
export interface AdminSummary extends InvoiceSummary {
  processing_by?: number
  requires_reconciliation?: boolean
}
export interface AdminDetail extends InvoiceDetail {
  processing_by?: number
  requires_reconciliation?: boolean
  reconciliation_reason?: string
}

const path = '/api/admin/invoice/applications'
export const pageSize = 20
export const maxPdfSize = 5 * 1024 * 1024
export const reviewKey = ['invoice-finance-review'] as const

interface Envelope<T> {
  success: boolean
  message?: string
  data?: T
}

function result<T>(envelope: Envelope<T>): T {
  if (!envelope?.success || envelope.data == null) {
    throw createServerError(envelope)
  }
  return envelope.data
}

export async function listInvoices(
  page: number,
  status: ReviewFilter,
  signal: AbortSignal
) {
  const { data } = await api.get<Envelope<InvoicePage<AdminSummary>>>(path, {
    params: { p: page, size: pageSize, status },
    signal,
    disableDuplicate: true,
  })
  return result(data)
}

export async function getInvoice(id: number, signal: AbortSignal) {
  const { data } = await api.get<Envelope<AdminDetail>>(`${path}/${id}`, {
    signal,
    disableDuplicate: true,
  })
  const detail = result(data)
  if (detail.id !== id) throw createServerError(null)
  return detail
}

export type ReviewDecision =
  | { kind: 'reject' | 'reconciliation'; reason: string }
  | { kind: 'claim' }
  | { kind: 'release'; confirmedNotIssued: true }
  | { kind: 'record'; number: string; verified: true; file: File }

export async function saveDecision(id: number, decision: ReviewDecision) {
  // Do not replay a financial review after a session refresh.
  const config = { singleUseAuthorization: true }
  if (decision.kind === 'claim' || decision.kind === 'release') {
    const body =
      decision.kind === 'release'
        ? { confirm_not_issued: decision.confirmedNotIssued }
        : undefined
    const { data } = await api.post<Envelope<InvoiceSummary>>(
      `${path}/${id}/${decision.kind}`,
      body,
      config
    )
    result(data)
    return
  }
  if (decision.kind !== 'record') {
    const { data } = await api.post<Envelope<InvoiceSummary>>(
      `${path}/${id}/${decision.kind}`,
      { reason: decision.reason },
      config
    )
    result(data)
    return
  }
  const form = new FormData()
  form.set('invoice_number', decision.number)
  form.set('confirm_payment_verified', String(decision.verified))
  form.set('file', decision.file)
  const { data } = await api.post<Envelope<InvoiceSummary>>(
    `${path}/${id}/issue`,
    form,
    config
  )
  result(data)
}

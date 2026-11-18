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
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  ApplicationList,
  ApplicationReview,
  CommerceConfig,
  EligibleInvoiceOrders,
  InvoiceApplication,
  InvoiceInput,
  InvoicePaymentOptions,
  InvoicePaymentResult,
  PartnerApplication,
  PartnerInput,
} from './types'

type Response<T> = { success: boolean; message: string; data: T }

export async function getCommerceConfig(
  admin = false
): Promise<CommerceConfig> {
  const response = await api.get<Response<CommerceConfig>>(
    `${admin ? '/api/admin' : '/api'}/commerce/config`
  )
  return requireServerSuccess(response.data).data
}

export async function saveCommerceConfig(
  config: CommerceConfig
): Promise<CommerceConfig> {
  const response = await api.put<Response<CommerceConfig>>(
    '/api/admin/commerce/config',
    config
  )
  return requireServerSuccess(response.data).data
}

export async function getEligibleInvoiceOrders(): Promise<EligibleInvoiceOrders> {
  const response = await api.get<Response<EligibleInvoiceOrders>>(
    '/api/invoices/eligible-orders'
  )
  return requireServerSuccess(response.data).data
}

export async function listInvoices(
  admin: boolean,
  status: string,
  page: number
): Promise<ApplicationList<InvoiceApplication>> {
  const response = await api.get<Response<ApplicationList<InvoiceApplication>>>(
    `${admin ? '/api/admin' : '/api'}/invoices`,
    { params: { status: status || undefined, p: page, page_size: 20 } }
  )
  return requireServerSuccess(response.data).data
}

export async function getInvoice(
  admin: boolean,
  id: number
): Promise<InvoiceApplication> {
  const response = await api.get<Response<InvoiceApplication>>(
    `${admin ? '/api/admin' : '/api'}/invoices/${id}`
  )
  return requireServerSuccess(response.data).data
}

export async function createInvoice(
  input: InvoiceInput,
  requestKey: string
): Promise<InvoiceApplication> {
  const response = await api.post<Response<InvoiceApplication>>(
    '/api/invoices',
    input,
    { headers: { 'Idempotency-Key': requestKey } }
  )
  return requireServerSuccess(response.data).data
}

export async function getInvoicePaymentOptions(input: {
  invoiceId?: number
  currency: string
  exponent: number
  feeMinor: string
}): Promise<InvoicePaymentOptions> {
  const response = await api.get<Response<InvoicePaymentOptions>>(
    input.invoiceId
      ? `/api/invoices/${input.invoiceId}/payment-options`
      : '/api/invoices/payment-options',
    {
      params: {
        currency: input.currency,
        currency_exponent: input.exponent,
        fee_minor: input.feeMinor,
      },
    }
  )
  return requireServerSuccess(response.data).data
}

export async function payInvoiceFee(
  id: number,
  method: string,
  revision: number,
  requestKey: string,
  expectedWalletQuota?: string
): Promise<InvoicePaymentResult> {
  const response = await api.post<Response<InvoicePaymentResult>>(
    `/api/invoices/${id}/pay`,
    {
      method,
      expected_revision: revision,
      expected_wallet_quota: expectedWalletQuota,
    },
    { headers: { 'Idempotency-Key': requestKey } }
  )
  return requireServerSuccess(response.data).data
}

export async function reviewInvoice(
  admin: boolean,
  id: number,
  input: ApplicationReview
): Promise<InvoiceApplication> {
  const response = await api.post<Response<InvoiceApplication>>(
    `${admin ? '/api/admin' : '/api'}/invoices/${id}/${admin ? 'review' : 'cancel'}`,
    input
  )
  return requireServerSuccess(response.data).data
}

export async function listPartners(
  admin: boolean,
  status: string,
  page: number
): Promise<ApplicationList<PartnerApplication>> {
  const response = await api.get<Response<ApplicationList<PartnerApplication>>>(
    `${admin ? '/api/admin' : '/api'}/partner-applications`,
    { params: { status: status || undefined, p: page, page_size: 20 } }
  )
  return requireServerSuccess(response.data).data
}

export async function getPartner(
  admin: boolean,
  id: number
): Promise<PartnerApplication> {
  const response = await api.get<Response<PartnerApplication>>(
    `${admin ? '/api/admin' : '/api'}/partner-applications/${id}`
  )
  return requireServerSuccess(response.data).data
}

export async function createPartner(
  input: PartnerInput,
  requestKey: string
): Promise<PartnerApplication> {
  const response = await api.post<Response<PartnerApplication>>(
    '/api/partner-applications',
    input,
    { headers: { 'Idempotency-Key': requestKey } }
  )
  return requireServerSuccess(response.data).data
}

export async function reviewPartner(
  admin: boolean,
  id: number,
  input: ApplicationReview
): Promise<PartnerApplication> {
  const response = await api.post<Response<PartnerApplication>>(
    `${admin ? '/api/admin' : '/api'}/partner-applications/${id}/${admin ? 'review' : 'cancel'}`,
    input
  )
  return requireServerSuccess(response.data).data
}

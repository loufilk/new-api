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
export type CommerceConfig = {
  revision: number
  invoice_enabled: boolean
  invoice_window_days: number
  invoice_max_orders: number
  invoice_currency: string
  general_fee_bps: number
  special_invoice_enabled: boolean
  special_fee_bps: number
  invoice_instructions: string
  partner_enabled: boolean
  partner_instructions: string
  updated_at: number
  updated_by: number
}

export type ApplicationEvent = {
  id: number
  action: string
  from_status: string
  to_status: string
  note?: string
  created_at: number
}

export type InvoiceOrder = {
  source_type?: 'topup' | 'redemption'
  redemption_id?: number
  topup_id: number
  trade_no: string
  currency: string
  currency_exponent: number
  amount_minor: string
  completed_at: number
}

export type InvoiceInput = {
  order_ids: number[]
  redemption_ids?: number[]
  category?: 'technical_service' | 'software_development'
  kind: 'general' | 'special'
  title_type: 'individual' | 'company'
  title: string
  tax_id: string
  email: string
  address: string
  phone: string
  bank_name: string
  bank_account: string
}

export type InvoiceApplication = Omit<
  InvoiceInput,
  'order_ids' | 'redemption_ids'
> & {
  id: number
  user_id: number
  currency: string
  currency_exponent: number
  amount_minor: string
  fee_minor: string
  fee_bps: number
  fee_receipt_reference: string
  fee_refund_status: '' | 'pending' | 'refunded'
  fee_refund_reference: string
  fee_payment?: InvoiceFeePayment
  status:
    | 'awaiting_fee'
    | 'submitted'
    | 'approved'
    | 'issued'
    | 'rejected'
    | 'cancelled'
  revision: number
  review_note: string
  invoice_number: string
  delivery_url: string
  created_at: number
  updated_at: number
  orders: InvoiceOrder[]
  events?: ApplicationEvent[]
}

export type InvoiceSummary = {
  currency: string
  currency_exponent: number
  available: { amount_minor: string; count: number }
  processing: { amount_minor: string; count: number }
  issued: { amount_minor: string; count: number }
}

export type EligibleInvoiceOrders = {
  items: InvoiceOrder[]
  config: CommerceConfig
  summary: InvoiceSummary
}

export type InvoicePaymentMethod = {
  type: string
  name: string
  provider: 'wallet' | 'epay'
}

export type InvoiceFeePayment = {
  method: string
  provider: 'wallet' | 'epay'
  status: string
  amount_minor: string
  currency: string
  currency_exponent: number
  wallet_quota: string
  trade_no: string
  paid_at: number
  refund_status: string
  refunded_at: number
}

export type InvoicePaymentOptions = {
  methods: InvoicePaymentMethod[]
  wallet_quota: string
  wallet_balance_quota: string
  currency: string
  currency_exponent: number
  fee_minor: string
  payment?: InvoiceFeePayment
}

export type InvoicePaymentResult = {
  invoice: InvoiceApplication
  payment: InvoiceFeePayment
  url?: string
  params?: Record<string, string>
}

export type PartnerQuote = {
  group: string
  models: string
  rate: string
  description: string
}

export type PartnerInput = {
  site_name: string
  site_url: string
  contact_name: string
  contact_email: string
  contact_method: string
  description: string
  quotes: PartnerQuote[]
}

export type PartnerApplication = PartnerInput & {
  id: number
  user_id: number
  status: 'submitted' | 'reviewing' | 'approved' | 'rejected' | 'cancelled'
  revision: number
  review_note: string
  created_at: number
  updated_at: number
  events?: ApplicationEvent[]
}

export type ApplicationReview = {
  expected_revision: number
  action:
    | 'cancel'
    | 'confirm_fee'
    | 'record_fee_refund'
    | 'approve'
    | 'reject'
    | 'issue'
    | 'review'
  note?: string
  invoice_number?: string
  delivery_url?: string
  fee_receipt_reference?: string
  fee_refund_reference?: string
}

export type ApplicationList<T> = { items: T[]; total: number }

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
import { useTranslation } from 'react-i18next'

import { formatMinorMoney } from '@/features/agent-platform/lib/money'
import { toIntlLocale } from '@/i18n/languages'

import type { InvoiceSummary } from '../contracts'

export const INVOICE_TEXT_KEYS = [
  'For contact only. Invoices are not emailed automatically. Download issued invoices from My applications.',
  'Refresh',
  'Refreshing…',
  'Order selection cleared. Review the refreshed list before submitting.',
  'Invoice center',
  'Available to invoice',
  'Pending invoice amount',
  'Issued invoice amount',
  '{{n}} orders',
  '{{n}} applications',
  'Eligible orders',
  'Select orders paid within the last 30 days. Up to 100 orders per application.',
  'Orders not listed?',
  'Contact support for manual review.',
  'No eligible orders',
  'Select this page',
  'Selection limit reached. Deselect orders before adding more.',
  'Order number',
  'Paid at',
  'Paid amount',
  'Invoice information',
  'Invoice type',
  'Ordinary invoice',
  'Special VAT invoice',
  'Only company invoices are available. Enter the registered company name and tax ID.',
  'Title type',
  'Company',
  'Individual',
  'Invoice title',
  'Tax ID',
  'Invoice category',
  'Select an invoice category',
  'Technical services',
  'Software development',
  'Email address',
  'Registered address',
  'Registered phone',
  'Bank name',
  'Bank account number',
  'Required',
  'Invalid field',
  'Enter a valid email address.',
  'Enter a 15–20 character tax ID using letters and digits.',
  'Enter a valid phone number.',
  'Enter the bank account using digits only.',
  'Special invoices require a company title.',
  'Selected orders',
  'Estimated invoice fee',
  'Configured tax rate (reference only)',
  'Fee at application',
  'Fee rate at application',
  'Configured tax rate at application',
  'This application requires a fee. It cannot be submitted until fee payment is available.',
  'This invoice type is currently unavailable',
  'Invoice amount',
  'Submit and choose payment',
  'Submitting will not charge you. Choose a payment method in the application details.',
  'Awaiting fee payment',
  'Fee payment status',
  'Unpaid',
  'Payment in progress',
  'Paid',
  'Refunded',
  'Fee refund review',
  'Paid invoice fee',
  'Fee paid at',
  'Fee payment method',
  'Choose a payment method',
  'Paid wallet balance',
  'Pay {{fee}} with {{method}}',
  'Use {{quota}} paid quota units to pay {{fee}}',
  'Only paid quota is used, not bonus or reward quota.',
  'Complete payment with the provider, then refresh the status here.',
  'Refresh payment status',
  'Check the payment status before trying again.',
  'No payment method is currently available.',
  'If cancelled, the fee will be returned to your paid balance.',
  'If cancelled, your fee refund will require manual review.',
  'Submit application',
  'Submitting…',
  'Application submitted.',
  'New applications are currently closed. You can still view and manage previous applications below.',
  'My applications',
  'All statuses',
  'Application status',
  'Pending review',
  'In progress',
  'Invoice processing',
  'Issued',
  'Rejected',
  'Cancelled',
  'No applications found',
  'Application',
  'Details',
  'Application details',
  'Submitted at',
  'Issued at',
  'Included orders',
  'Invoice number',
  'Rejection reason',
  'Download PDF',
  'Cancel application',
  'Cancel this application?',
  'Eligible orders in this application will become available again.',
  'Keep application',
  'Application cancelled.',
  'Previous page',
  'Next page',
  'Page {{page}} of {{pages}}',
  'Retry',
  'Unable to load invoice information',
] as const

export type InvoiceTextKey = (typeof INVOICE_TEXT_KEYS)[number]
export function useInvoiceText() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const text = (
    key: InvoiceTextKey,
    vars?: Record<string, string | number>
  ): string => t(key, { ...vars })
  return {
    text,
    locale,
    money: (cents: string) => formatMinorMoney(cents, 'CNY', 2, locale),
  }
}

export const statusLabels = {
  awaiting_payment: 'Awaiting fee payment',
  pending: 'Pending review',
  issued: 'Issued',
  rejected: 'Rejected',
  cancelled: 'Cancelled',
} as const
export function applicationStatusLabel(entry: InvoiceSummary): InvoiceTextKey {
  if (entry.status === 'pending' && entry.processing) {
    return 'Invoice processing'
  }
  return statusLabels[entry.status]
}

export const invoiceTypeLabels = {
  ordinary: 'Ordinary invoice',
  special: 'Special VAT invoice',
} as const
export const titleTypeLabels = {
  company: 'Company',
  personal: 'Individual',
} as const

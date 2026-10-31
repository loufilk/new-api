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

import { StatusBadge } from '@/components/status-badge'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatNumber, formatTimestampToDate } from '@/lib/format'

import { formatMinorMoney } from '../lib/money'

export function Money(props: {
  minor: string
  currency: string
  exponent: number
}) {
  const { i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return (
    <span className='whitespace-nowrap tabular-nums'>
      {formatMinorMoney(props.minor, props.currency, props.exponent, locale)}
    </span>
  )
}

export function Quota(props: { value: number }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return (
    <span
      className='tabular-nums'
      title={t('{{count}} quota units', {
        count: props.value,
        formatted: formatNumber(props.value, locale),
      })}
    >
      {formatQuotaWithCurrency(props.value, { abbreviate: false, locale })}
    </span>
  )
}

export function BusinessState(props: { value: string }) {
  const { t } = useTranslation()
  const labels: Record<string, string> = {
    active: t('Active'),
    exited: t('Exited'),
    pending: t('Pending'),
    applied: t('Awaiting approval'),
    approved: t('Approved'),
    rejected: t('Rejected'),
    paying: t('Payment in progress'),
    succeeded: t('Succeeded'),
    failed: t('Failed'),
    explicit_failed: t('Confirmed unpaid'),
    confirmed: t('Confirmed'),
    done: t('Completed'),
    spent: t('Used'),
    refunded: t('Refunded quota'),
    refund_frozen: t('Refund frozen quota'),
    reclaimed: t('Reclaimed quota'),
    reclassified: t('Reclassified quota'),
    reclass_frozen: t('Quota pending reclassification'),
    unknown: t('Payment result unknown'),
    awaiting_payment: t('Awaiting payment'),
    payment_submitted: t('Payment evidence submitted'),
    evidence_submitted: t('Payment evidence submitted'),
    credited: t('Credited'),
    verified: t('Awaiting independent review'),
    completed: t('Completed'),
    requested: t('Awaiting approval'),
    payment_pending: t('Payment in progress'),
    payment_unknown: t('Payment result unknown'),
    created: t('Awaiting customer claim'),
    claimed: t('Bound to customer'),
    available: t('Available'),
    bound: t('Bound to customer'),
    redeemed: t('Redeemed'),
    cancelled: t('Cancelled'),
    matured: t('Matured'),
    reserved: t('Customer delivery reservations'),
    purchase: t('Paid procurement'),
    commission_transfer: t('Commission conversion'),
    promotion: t('Promotional grant'),
    paid: t('Paid procurement'),
    commission: t('Commission conversion'),
    legacy: t('Legacy budget'),
    promotional: t('Promotional grant'),
  }
  let variant: 'neutral' | 'success' | 'danger' = 'neutral'
  if (
    [
      'succeeded',
      'completed',
      'redeemed',
      'active',
      'available',
      'matured',
    ].includes(props.value)
  ) {
    variant = 'success'
  }
  if (
    ['unknown', 'payment_unknown', 'failed', 'rejected'].includes(props.value)
  ) {
    variant = 'danger'
  }
  return (
    <StatusBadge
      label={labels[props.value] ?? props.value}
      variant={variant}
      copyable={false}
    />
  )
}

export function BusinessDate(props: { value: number }) {
  useTranslation()
  return (
    <span className='whitespace-nowrap'>
      {props.value ? formatTimestampToDate(props.value) : '—'}
    </span>
  )
}

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
import { ledgerReasonLabelKey } from '@/extensions/chuanapi/wallet/lib/ledger'
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
    not_submitted: t('Qualification not submitted'),
    approval_required: t('Qualification approval required'),
    review_expired: t('Qualification review expired'),
    long_inactivity: t('Review required after inactivity'),
    campaign_grant: t('Campaign grant'),
    campaign_expiry: t('Benefit expired'),
    campaign_revoke: t('Benefit revoked'),
    legacy_debit: t('Legacy source debit'),
    unclassified_debit: t('Unclassified source debit'),
    task_owned: t('Asynchronous task owns settlement'),
    refund_pending: t('Historical refund pending review'),
    refund_ready_v2: t('Verified refund intent'),
    refund_review_v2: t('Refund evidence requires review'),
    settlement_review_v2: t('Settlement evidence requires review'),
    NOT_START: t('Not started'),
    SUBMITTED: t('Submitted'),
    QUEUED: t('Queued'),
    IN_PROGRESS: t('In progress'),
    SUCCESS: t('Task succeeded'),
    FAILURE: t('Task failed'),
    UNKNOWN: t('Task result unknown'),
    duplicate_conflict: t('Contradictory duplicate transaction'),
    exclude_invalid_import: t(
      'Exclude an erroneous import with verified replacement'
    ),
    original_payer_dossier: t('Original verified payer dossier'),
    original_provider_payment: t('Original provider payment'),
    live: t('Live'),
    credit: t('Credit-funded quota'),
    source_spend: t('Recognize when credit-funded quota is spent'),
    none: t('No interest or compounding'),
    recorded: t('Awaiting independent review'),
    open: t('Open'),
    settled: t('Settled'),
    overdue: t('Overdue'),
    write_off: t('Write off without repayment'),
    reverse_write_off: t('Reverse prior write-off'),
    correction_credit: t('Correct receivable principal'),
    offset: t('Deposit offset'),
    refund: t('Deposit return'),
    proposed: t('Awaiting independent review'),
    closed: t('Closed'),
    unmatched: t('Unmatched'),
    exception: t('Exception requires review'),
    excluded: t('Excluded after review'),
    incoming: t('Incoming'),
    outgoing: t('Outgoing'),
    agreement: t('Individual agent agreement'),
    tier: t('Global tier schedule'),
    quota: t('Quota units'),
    face_minor: t('Face amount in minor units'),
    base_paid_minor: t('Base-rate amount in minor units'),
    review_pending: t('Awaiting campaign review'),
    granted: t('Benefit granted'),
    expired: t('Expired'),
    revoked: t('Revoked'),
    restore_original_expiry: t('Restore the original source and expiry only'),
    legacy_nonexpiring: t('Legacy benefit without expiry'),
    campaign: t('Campaign grant'),
    feature_disabled: t('Disabled by current policy'),
    not_approved: t('Awaiting approval'),
    not_started: t('Not started'),
    claim_window_closed: t('Claim window closed'),
    linuxdo_binding_required: t('Verified LinuxDO binding required'),
    agent_invitation_required: t('Agent invitation required'),
    already_claimed: t('Already claimed'),
    eligible_subject_to_budget: t('Eligible subject to budget and risk checks'),
    total: t('Total'),
    daily: t('Daily'),
    agent: t('Agent'),
    personal_agreement: t('Individual agreement matched'),
    global_tier_base: t('Global tier schedule matched'),
    global_policy: t('Global policy matched'),
    exception_approved: t('Approved by documented exception'),
    direction_conflict: t('Transaction direction mismatch'),
    business_conflict: t('Business reference mismatch'),
    currency_conflict: t('Currency mismatch'),
    amount_conflict: t('Amount mismatch'),
    state_conflict: t('Business payment is not confirmed'),
    external_conflict: t('Bank transaction reference mismatch'),
    account_conflict: t('Bank account dossier mismatch'),
    beneficiary_conflict: t('Verified beneficiary mismatch'),
    duplicate: t('Duplicate transaction'),
    unallocated: t('Unallocated transaction'),
    retry: t('Retry matching after correction'),
    exclude_duplicate: t('Exclude confirmed duplicate transaction'),
    exclude_unrelated: t('Exclude verified unrelated transaction'),
    incoming_receipt: t('Incoming receipt'),
    outgoing_receipt: t('Outgoing receipt'),
    credit_repayment: t('Credit repayment'),
    deposit: t('Deposit receipt'),
    deposit_refund: t('Deposit return'),
    purchase_refund: t('Purchase refund'),
    withdrawal: t('Commission withdrawal'),

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
      label={labels[props.value] ?? t(ledgerReasonLabelKey(props.value))}
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

/** Gross risk usage can exceed the JSON-safe integer range; never coerce it to Number. */
export function ExactQuota(props: { value: string }) {
  const { t, i18n } = useTranslation()
  if (!/^(0|[1-9]\d*)$/.test(props.value)) return <span>—</span>
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return (
    <span className='tabular-nums'>
      {new Intl.NumberFormat(locale).format(BigInt(props.value))}{' '}
      {t('Quota units')}
    </span>
  )
}

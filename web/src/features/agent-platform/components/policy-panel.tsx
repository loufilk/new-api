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
import { useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from '../api'
import { useRequestId } from '../hooks/use-request-id'
import { currencyOptions, validCurrencyExponent } from '../lib/currencies'
import type { AgentPolicy, OperationDraft, OperationPayload } from '../types'
import { ActionForm, type ActionField } from './action-form'
import { expansionPolicyFields } from './expansion-policy-fields'
import { OperationDialog } from './operation-dialog'
import { ResourceTable } from './resource-table'
import { BusinessDate } from './values'

const policyFields: ActionField[] = [
  ...expansionPolicyFields,
  {
    name: 'legacy_purchase_reclassification_enabled',
    label: 'Enable verified legacy purchase reclassification',
    kind: 'checkbox',
  },
  {
    name: 'purchase_refund_terms_approved',
    label: 'Original purchase refund terms approved',
    kind: 'checkbox',
    description:
      'Approve the refund waiting period and fee before enabling sales. Sold batches retain these terms; a later feature switch does not erase existing refund rights.',
  },
  { name: 'reason', label: 'Reason for policy version', kind: 'text' },
  {
    name: 'risk_policy_approved',
    label: 'Commercial and risk policy approved',
    kind: 'checkbox',
  },
  {
    name: 'purchase_enabled',
    label: 'Enable new procurement',
    kind: 'checkbox',
  },
  {
    name: 'delivery_enabled',
    label: 'Enable new deliveries',
    kind: 'checkbox',
  },
  {
    name: 'purchase_refund_enabled',
    label: 'Enable new purchase refunds',
    kind: 'checkbox',
  },
  {
    name: 'procurement_self_use_enabled',
    label: 'Enable procurement self-use',
    kind: 'checkbox',
  },
  {
    name: 'purchase_currency',
    label: 'Procurement currency (ISO code)',
    kind: 'select',
    options: currencyOptions('purchase_currency_exponent'),
    required: false,
  },
  {
    name: 'purchase_currency_exponent',
    readOnly: true,
    description: 'Decimal places are fixed by the selected original currency.',
    label: 'Procurement currency decimal places',
    kind: 'integer',
    max: 6,
  },
  {
    name: 'purchase_rate_bps',
    label: 'Procurement price (basis points)',
    kind: 'integer',
    max: 10000,
  },
  {
    name: 'purchase_quota_numerator',
    label: 'Procurement quota conversion numerator',
    kind: 'integer',
  },
  {
    name: 'purchase_quota_denominator',
    label: 'Procurement quota conversion denominator',
    kind: 'integer',
  },
  {
    name: 'purchase_minimum_quota',
    label: 'Minimum procurement quota units',
    kind: 'integer',
  },
  {
    name: 'purchase_maximum_quota',
    label: 'Maximum procurement quota units',
    kind: 'integer',
  },
  {
    name: 'quote_lifetime_seconds',
    label: 'Quote lifetime (seconds)',
    kind: 'integer',
  },
  {
    name: 'refund_waiting_seconds',
    label: 'Refund waiting period (seconds)',
    kind: 'integer',
  },
  {
    name: 'refund_fee_bps',
    label: 'Purchase refund fee (basis points)',
    kind: 'integer',
    max: 10000,
  },
  {
    name: 'commission_enabled',
    label: 'Enable new commission accrual',
    kind: 'checkbox',
  },
  {
    name: 'commission_currency',
    label: 'Commission currency (ISO code)',
    kind: 'select',
    options: currencyOptions('commission_currency_exponent'),
    required: false,
  },
  {
    name: 'commission_currency_exponent',
    readOnly: true,
    description: 'Decimal places are fixed by the selected original currency.',
    label: 'Commission currency decimal places',
    kind: 'integer',
    max: 6,
  },
  {
    name: 'commission_user_rate_bps',
    label: 'Ordinary inviter commission (basis points)',
    kind: 'integer',
    max: 10000,
  },
  {
    name: 'commission_agent_rate_bps',
    label: 'Agent commission (basis points)',
    kind: 'integer',
    max: 10000,
  },
  {
    name: 'commission_referral_cap_minor',
    label: 'Lifetime referral cap (minor units)',
    kind: 'minor',
  },
  {
    name: 'commission_maturity_seconds',
    label: 'Commission maturity period (seconds)',
    kind: 'integer',
  },
  {
    name: 'commission_reserve_bps',
    label: 'Commission risk reserve (basis points)',
    kind: 'integer',
    max: 10000,
  },
  {
    name: 'withdrawal_enabled',
    label: 'Enable new withdrawals',
    kind: 'checkbox',
  },
  {
    name: 'commission_conversion_enabled',
    label: 'Enable commission conversions',
    kind: 'checkbox',
  },
  {
    name: 'withdrawal_minimum_minor',
    label: 'Minimum withdrawal (minor units)',
    kind: 'minor',
  },
  {
    name: 'withdrawal_fee_bps',
    label: 'Withdrawal fee (basis points)',
    kind: 'integer',
    max: 10000,
  },
  {
    name: 'withdrawal_interval_seconds',
    label: 'Minimum withdrawal interval (seconds)',
    kind: 'integer',
  },
  {
    name: 'withdrawal_calendar_month_limit',
    label: 'Withdrawals per calendar month',
    kind: 'integer',
    max: 1000,
  },
  {
    name: 'conversion_quota_numerator',
    label: 'Commission conversion quota numerator',
    kind: 'integer',
  },
  {
    name: 'conversion_quota_denominator',
    label: 'Commission conversion quota denominator',
    kind: 'integer',
  },
]

export function PolicyPanel(props: { finance?: boolean }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const requestId = useRequestId()
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  const query = useQuery({
    queryKey: ['agent-platform', user?.id, 'policy'],
    queryFn: ({ signal }) =>
      getPlatform<AgentPolicy>('/policy/current', undefined, signal),
  })
  const columns: ColumnDef<AgentPolicy>[] = [
    {
      accessorKey: 'id',
      header: t('Policy version'),
      meta: { mobileTitle: true },
    },
    { accessorKey: 'created_by', header: t('Operator ID') },
    {
      accessorKey: 'created_at',
      header: t('Created'),
      cell: ({ row }) => <BusinessDate value={row.original.created_at} />,
    },
    { accessorKey: 'reason', header: t('Reason') },
  ]
  if (query.isPending) return <LoadingState />
  if (query.isError || !query.data) {
    return (
      <ErrorState
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }
  const policy = query.data
  const flags = policyFields.filter((field) => field.kind === 'checkbox')
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertTitle>
          {t('Policy version')} {policy.id}
        </AlertTitle>
        <AlertDescription>
          {t(
            'New financial features are disabled until an explicit policy is approved. Disabling new business does not cancel existing customer entitlements, historical consumption benefits or payments in progress.'
          )}
        </AlertDescription>
      </Alert>
      <dl className='grid grid-cols-1 gap-4 sm:grid-cols-2'>
        {flags.map((field) => (
          <div key={field.name}>
            <dt>{t(field.label)}</dt>
            <dd>
              {policy[field.name as keyof AgentPolicy] === true
                ? t('Enabled')
                : t('Disabled')}
            </dd>
          </div>
        ))}
        {policyFields
          .filter((field) => field.kind !== 'checkbox')
          .map((field) => (
            <div key={field.name}>
              <dt>{t(field.label)}</dt>
              <dd className='break-all'>
                {String(policy[field.name as keyof AgentPolicy] ?? '—')}
              </dd>
            </div>
          ))}
        <div>
          <dt>{t('Commission cutover')}</dt>
          <dd>
            <BusinessDate value={policy.commission_cutover_at} />
          </dd>
        </div>
      </dl>
      <p className='text-muted-foreground text-sm'>
        {t(
          'All external payouts require manual review and offline human payment. There is no automatic payout API. New credit, pricing and promotional terms require explicit approval.'
        )}
      </p>
      {props.finance && user?.role === ROLE.SUPER_ADMIN && (
        <Button className='self-start' onClick={() => setEditing(true)}>
          {t('Publish a new policy version')}
        </Button>
      )}
      {props.finance && (
        <ResourceTable<AgentPolicy>
          resource='/finance/policies'
          columns={columns}
        />
      )}
      {editing && (
        <ActionForm
          title={t('Publish a new policy version')}
          description={t(
            'All rates are basis points (10,000 = 100%). Time periods are seconds. Cash thresholds are integer minor units of the configured currency. No commercial defaults are assumed.'
          )}
          fields={policyFields}
          initial={{
            ...Object.fromEntries(
              policyFields.map((field) => [
                field.name,
                field.kind === 'checkbox'
                  ? policy[field.name as keyof AgentPolicy] === true
                  : String(policy[field.name as keyof AgentPolicy] ?? ''),
              ])
            ),
            reason: '',
          }}
          onClose={() => setEditing(false)}
          onSubmit={(values) => {
            const next: OperationPayload = {}
            for (const field of policyFields) {
              let value: string | number | boolean = String(values[field.name])
              if (field.kind === 'checkbox') value = values[field.name] === true
              else if (field.kind === 'integer') {
                value = Number(values[field.name])
              }
              next[field.name] = value
            }
            for (const prefix of [
              'purchase',
              'commission',
              'credit',
              'deposit',
            ]) {
              const currency = String(next[`${prefix}_currency`])
              if (
                currency &&
                !validCurrencyExponent(
                  currency,
                  Number(next[`${prefix}_currency_exponent`])
                )
              ) {
                throw new Error(
                  t(
                    'Original currency and decimal places do not match the supported currency catalog.'
                  )
                )
              }
            }
            const payload = { expected_version: policy.id, policy: next }
            setDraft({
              action: 'policy.publish',
              request_id: requestId(payload),
              payload,
              endpoint: '/finance/policies',
              resource: '/finance/policies',
              review: policyFields
                .filter(
                  (field) =>
                    String(policy[field.name as keyof AgentPolicy]) !==
                    String(next[field.name])
                )
                .map((field) => ({
                  label: field.label,
                  before: String(policy[field.name as keyof AgentPolicy]),
                  after: String(next[field.name]),
                })),
            })
            setEditing(false)
          }}
        />
      )}
      {draft && (
        <OperationDialog
          key={draft.request_id}
          draft={draft}
          onClose={() => setDraft(null)}
          onComplete={() => {
            requestId.reset()
            setDraft(null)
            void query.refetch()
          }}
        />
      )}
    </div>
  )
}

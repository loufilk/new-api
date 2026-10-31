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
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from '../api'
import { useRequestId } from '../hooks/use-request-id'
import { currencyOptions, validCurrencyExponent } from '../lib/currencies'
import { parseMinorMoney } from '../lib/money'
import type { AgentPolicy, OperationDraft } from '../types'
import { ActionForm, type ActionField } from './action-form'
import { OperationDialog } from './operation-dialog'
import { ResourceTable } from './resource-table'
import { BusinessDate, BusinessState, Money, Quota } from './values'

interface FundLedger {
  id: number
  agent_user_id: number
  batch_id: number
  quota: number
  cost_minor: string
  currency: string
  currency_exponent: number
  from_state: string
  to_state: string
  redemption_id: number
  refund_id: number
  reclassification_id: number
  created_at: number
}
export function FundLedgerTable() {
  const { t } = useTranslation()
  const columns: ColumnDef<FundLedger>[] = [
    { accessorKey: 'id', header: t('Ledger ID'), meta: { mobileTitle: true } },
    { accessorKey: 'batch_id', header: t('Funding batch ID') },
    {
      accessorKey: 'quota',
      header: t('Quota'),
      cell: ({ row }) => <Quota value={row.original.quota} />,
    },
    {
      accessorKey: 'cost_minor',
      header: t('Interval source book cost'),
      cell: ({ row }) =>
        row.original.currency ? (
          <Money
            minor={row.original.cost_minor}
            currency={row.original.currency}
            exponent={row.original.currency_exponent}
          />
        ) : (
          <span>
            {row.original.cost_minor} {t('(minor units)')}
          </span>
        ),
    },
    {
      accessorKey: 'from_state',
      header: t('From state'),
      cell: ({ row }) => <BusinessState value={row.original.from_state} />,
    },
    {
      accessorKey: 'to_state',
      header: t('To state'),
      cell: ({ row }) => <BusinessState value={row.original.to_state} />,
    },
    { accessorKey: 'redemption_id', header: t('Redemption ID') },
    { accessorKey: 'refund_id', header: t('Refund ID') },
    { accessorKey: 'reclassification_id', header: t('Reclassification ID') },
    {
      accessorKey: 'created_at',
      header: t('Created'),
      cell: ({ row }) => <BusinessDate value={row.original.created_at} />,
    },
  ]
  return (
    <div className='space-y-4'>
      <Alert>
        <AlertDescription>
          {t(
            'This ledger preserves interval source book cost only. Actual cash refunds and credit receivables use their own confirmed cumulative totals; never treat this book cost as payable cash or debt.'
          )}
        </AlertDescription>
      </Alert>
      <ResourceTable resource='/fund-ledger' columns={columns} />
    </div>
  )
}
interface FundAudit {
  anomalies: {
    id: number
    agent_user_id: number
    code: string
    detail: string
  }[]
  checked_agent_ids: number[]
  next_cursor: number
  checked_at: number
}
export function FundReconciliation() {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [cursor, setCursor] = useState(0)
  const query = useQuery({
    queryKey: ['agent-platform', userId, 'fund-reconciliation', cursor],
    queryFn: ({ signal }) =>
      getPlatform<FundAudit>(
        '/finance/funds/reconciliation',
        { after_agent_id: cursor, limit: 100 },
        signal
      ),
  })
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
  const report = query.data
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'This page checked {{count}} agents. A clean page is not proof that every account reconciles.',
            { count: report.checked_agent_ids.length }
          )}{' '}
          <BusinessDate value={report.checked_at} />
        </AlertDescription>
      </Alert>
      {report.anomalies.length === 0 && (
        <p>{t('No anomalies reported on this page.')}</p>
      )}
      {report.anomalies.map((anomaly) => (
        <Alert
          key={`${anomaly.id}-${anomaly.agent_user_id}-${anomaly.code}`}
          variant='destructive'
        >
          <AlertDescription>
            <p>
              {t('Agent ID')} {anomaly.agent_user_id} · {anomaly.code}
            </p>
            <p>{t(anomaly.detail)}</p>
          </AlertDescription>
        </Alert>
      ))}
      <div className='flex flex-wrap gap-2'>
        <Button
          variant='outline'
          onClick={() => {
            setCursor(0)
            void query.refetch()
          }}
        >
          {t('Restart reconciliation')}
        </Button>
        {report.next_cursor > 0 && (
          <Button onClick={() => setCursor(report.next_cursor)}>
            {t('Check next accounts')}
          </Button>
        )}
      </div>
    </div>
  )
}
interface Reclassification {
  id: number
  agent_user_id: number
  legacy_batch_id: number
  quota: number
  paid_minor: string
  currency: string
  currency_exponent: number
  state: string
  version: number
  verified_by: number
  reviewed_by: number
  fund_batch_id: number
  evidence_reference: string
  created_at: number
}
const recordFields: ActionField[] = [
  {
    name: 'historical_procurement_confirmed',
    label: 'I verified this was an actual historical procurement payment',
    kind: 'checkbox',
  },
  {
    name: 'not_wallet_topup_confirmed',
    label: 'I verified this payment was not credited to a consumption wallet',
    kind: 'checkbox',
  },
  { name: 'agent_user_id', label: 'Agent ID', kind: 'integer', min: 1 },
  {
    name: 'legacy_batch_id',
    label: 'Legacy funding batch ID',
    kind: 'integer',
    min: 1,
  },
  {
    name: 'quota',
    label: 'Quota units to reclassify',
    kind: 'integer',
    min: 1,
  },
  {
    name: 'currency',
    label: 'Original currency (ISO code)',
    kind: 'select',
    options: currencyOptions('currency_exponent'),
  },
  {
    name: 'currency_exponent',
    readOnly: true,
    description: 'Decimal places are fixed by the selected original currency.',
    label: 'Original currency decimal places',
    kind: 'integer',
    max: 6,
  },
  { name: 'paid_amount', label: 'Actual historical payment amount' },
  {
    name: 'provider',
    label: 'Original payment provider',
    kind: 'select',
    options: [
      'stripe',
      'creem',
      'epay',
      'waffo',
      'pancake',
      'bank_transfer',
    ].map((value) => ({ value, label: value })),
  },
  { name: 'merchant_scope', label: 'Original merchant scope' },
  {
    name: 'environment',
    label: 'Payment environment',
    kind: 'select',
    options: [{ value: 'live', label: 'Live' }],
  },
  {
    name: 'external_reference',
    label: 'Canonical provider payment reference',
    description:
      'Use the original payment identifier, not a new manual alias. An already credited receipt cannot create new procurement funds.',
  },
  { name: 'paid_at', label: 'Actual payment time', kind: 'datetime' },
  { name: 'evidence_reference', label: 'Payment evidence reference' },
]
export function FundReclassifications() {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [recording, setRecording] = useState(false)
  const [selected, setSelected] = useState<Reclassification | null>(null)
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  const requestId = useRequestId()
  const policy = useQuery({
    queryKey: ['agent-platform', userId, 'policy'],
    queryFn: ({ signal }) =>
      getPlatform<AgentPolicy>('/policy/current', undefined, signal),
  })
  const columns: ColumnDef<Reclassification>[] = [
    {
      accessorKey: 'id',
      header: t('Reclassification ID'),
      meta: { mobileTitle: true },
    },
    { accessorKey: 'agent_user_id', header: t('Agent ID') },
    { accessorKey: 'legacy_batch_id', header: t('Legacy funding batch ID') },
    {
      accessorKey: 'state',
      header: t('Status'),
      cell: ({ row }) => <BusinessState value={row.original.state} />,
    },
    {
      accessorKey: 'quota',
      header: t('Quota'),
      cell: ({ row }) => <Quota value={row.original.quota} />,
    },
    {
      accessorKey: 'paid_minor',
      header: t('Actual historical payment amount'),
      cell: ({ row }) => (
        <Money
          minor={row.original.paid_minor}
          currency={row.original.currency}
          exponent={row.original.currency_exponent}
        />
      ),
    },
    { accessorKey: 'verified_by', header: t('Receipt verifier') },
    { accessorKey: 'reviewed_by', header: t('Independent reviewer') },
    { accessorKey: 'fund_batch_id', header: t('Resulting funding batch') },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) =>
        row.original.state === 'verified' ? (
          <Button
            size='sm'
            disabled={
              row.original.verified_by === userId ||
              row.original.agent_user_id === userId
            }
            onClick={() => setSelected(row.original)}
          >
            {t('Independent review')}
          </Button>
        ) : null,
    },
  ]
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Reclassification requires historical payment evidence and an independent reviewer. It reserves existing available legacy quota and changes its source only; it does not mint new quota or turn free grants into cash.'
          )}
        </AlertDescription>
      </Alert>
      {policy.isError && (
        <ErrorState
          onRetry={() => {
            void policy.refetch()
          }}
        />
      )}
      <Button
        className='self-start'
        disabled={!policy.data?.legacy_purchase_reclassification_enabled}
        onClick={() => setRecording(true)}
      >
        {t('Record verified historical purchase')}
      </Button>
      {!policy.data?.legacy_purchase_reclassification_enabled && (
        <p>
          {t(
            'New historical purchase reclassification is disabled by policy. Existing pending reviews remain available.'
          )}
        </p>
      )}
      <ResourceTable
        resource='/finance/fund-reclassifications'
        columns={columns}
      />
      {recording && (
        <ActionForm
          title={t('Record verified historical purchase')}
          fields={recordFields}
          onClose={() => setRecording(false)}
          onSubmit={(values) => {
            if (
              values.historical_procurement_confirmed !== true ||
              values.not_wallet_topup_confirmed !== true
            ) {
              throw new Error(
                t(
                  'Verify historical procurement and exclude previously credited wallet top-ups before continuing.'
                )
              )
            }
            const exponent = Number(values.currency_exponent),
              paid = parseMinorMoney(String(values.paid_amount), exponent)
            if (
              !paid ||
              !validCurrencyExponent(String(values.currency), exponent)
            ) {
              throw new Error(
                t('Enter a valid amount in the original currency.')
              )
            }
            const payload = {
              historical_procurement_confirmed: true,
              not_wallet_topup_confirmed: true,
              agent_user_id: Number(values.agent_user_id),
              legacy_batch_id: Number(values.legacy_batch_id),
              quota: Number(values.quota),
              paid_minor: paid,
              currency: String(values.currency),
              currency_exponent: exponent,
              provider: String(values.provider),
              merchant_scope: String(values.merchant_scope).trim(),
              environment: String(values.environment),
              external_reference: String(values.external_reference).trim(),
              paid_at: Math.floor(Date.parse(String(values.paid_at)) / 1000),
              evidence_reference: String(values.evidence_reference).trim(),
            }
            setDraft({
              action: 'purchase.legacy.record',
              request_id: requestId(payload),
              payload,
              endpoint: '/finance/fund-reclassifications',
              resource: '/finance/fund-reclassifications',
              review: recordFields.map((field) => ({
                label: field.label,
                after: String(values[field.name]),
              })),
            })
            setRecording(false)
          }}
        />
      )}
      {selected && (
        <ActionForm
          title={t('Independent review')}
          fields={[
            {
              name: 'decision',
              label: 'Review decision',
              kind: 'select',
              options: [
                { value: 'approve', label: 'Approve' },
                { value: 'reject', label: 'Reject' },
              ],
            },
            {
              name: 'evidence_reference',
              label: 'Independent review evidence reference',
            },
          ]}
          onClose={() => setSelected(null)}
          onSubmit={(values) => {
            const payload = {
              reclassification_id: selected.id,
              version: selected.version,
              decision: String(values.decision),
              evidence_reference: String(values.evidence_reference).trim(),
            }
            setDraft({
              action: 'purchase.legacy.confirm',
              request_id: requestId(payload),
              payload,
              endpoint: `/finance/fund-reclassifications/${selected.id}/confirm`,
              resource: '/finance/fund-reclassifications',
              review: [
                { label: 'Review decision', after: String(values.decision) },
                {
                  label: 'Independent review evidence reference',
                  after: String(values.evidence_reference),
                },
              ],
            })
            setSelected(null)
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
          }}
        />
      )}
    </div>
  )
}

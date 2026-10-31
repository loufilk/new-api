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
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useAuthStore } from '@/stores/auth-store'

import { useRequestId } from '../hooks/use-request-id'
import type { OperationDraft, OperationPayload, PurchaseRefund } from '../types'
import { ActionForm, type ActionField } from './action-form'
import { ManualRefundInstructionsButton } from './manual-refund-instructions'
import { OperationDialog } from './operation-dialog'
import { ResourceTable } from './resource-table'
import { BusinessDate, BusinessState, Money, Quota } from './values'

export function PurchaseRefunds(props: { finance?: boolean }) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const requestId = useRequestId()
  const [action, setAction] = useState<{
    refund: PurchaseRefund
    kind: 'approve' | 'submit' | 'resolve'
  } | null>(null)
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  const path = props.finance ? '/finance/purchase-refunds' : '/purchase-refunds'
  const columns: ColumnDef<PurchaseRefund>[] = [
    { accessorKey: 'id', header: t('Refund ID'), meta: { mobileTitle: true } },
    {
      accessorKey: 'business_key',
      header: t('Payment business number'),
      cell: ({ row }) => (
        <span className='flex items-center gap-1 font-mono break-all'>
          {row.original.business_key}
          <CopyButton
            value={row.original.business_key}
            aria-label={t('Copy payment business number')}
          />
        </span>
      ),
    },
    {
      accessorKey: 'state',
      header: t('Status'),
      meta: { mobileBadge: true },
      cell: ({ row }) => (
        <BusinessState
          value={
            row.original.state === 'failed'
              ? 'explicit_failed'
              : row.original.state
          }
        />
      ),
    },
    { accessorKey: 'batch_id', header: t('Original batch') },
    {
      accessorKey: 'quota',
      header: t('Frozen quota'),
      cell: ({ row }) => <Quota value={row.original.quota} />,
    },
    ...(['principal_minor', 'fee_minor', 'net_minor'] as const).map((key) => ({
      accessorKey: key,
      header: t(
        {
          principal_minor: 'Original principal',
          fee_minor: 'Fee',
          net_minor: 'Net refund',
        }[key]
      ),
      cell: ({ row }: { row: { original: PurchaseRefund } }) => (
        <Money
          minor={row.original[key]}
          currency={row.original.currency}
          exponent={row.original.currency_exponent}
        />
      ),
    })),
    { accessorKey: 'approved_by', header: t('Approver') },
    {
      accessorKey: 'evidence_reference',
      header: t('Payment evidence reference'),
    },
    {
      accessorKey: 'created_at',
      header: t('Created'),
      cell: ({ row }) => <BusinessDate value={row.original.created_at} />,
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => {
        if (!props.finance || row.original.agent_user_id === userId) return null
        const refund = row.original
        if (refund.state === 'requested') {
          return (
            <Button
              size='sm'
              variant='outline'
              disabled={
                refund.requested_by === userId ||
                refund.agent_user_id === userId
              }
              onClick={() => setAction({ refund, kind: 'approve' })}
            >
              {t('Review refund')}
            </Button>
          )
        }
        const instructions = (
          <ManualRefundInstructionsButton
            kind='purchase'
            target={{ ...refund, amount_minor: refund.net_minor }}
          />
        )
        if (refund.state === 'approved') {
          return (
            <div className='flex flex-wrap gap-2'>
              {instructions}
              {refund.submitted_by === 0 &&
                refund.submitted_at === 0 &&
                refund.requested_by !== userId && (
                  <Button
                    size='sm'
                    variant='outline'
                    onClick={() => setAction({ refund, kind: 'approve' })}
                  >
                    {t('Reject')}
                  </Button>
                )}
              <Button
                size='sm'
                variant='outline'
                onClick={() => setAction({ refund, kind: 'submit' })}
              >
                {t('Record payment submission')}
              </Button>
            </div>
          )
        }
        if (
          refund.state === 'payment_pending' ||
          refund.state === 'payment_unknown'
        ) {
          return (
            <div className='flex flex-wrap gap-2'>
              {instructions}
              <Button
                size='sm'
                variant='outline'
                onClick={() => setAction({ refund, kind: 'resolve' })}
              >
                {t('Reconcile payment result')}
              </Button>
            </div>
          )
        }
        return null
      },
    },
  ]
  let fields: ActionField[] = []
  if (action?.kind === 'approve') {
    fields = [
      {
        name: 'decision',
        label: 'Review decision',
        kind: 'select',
        options: [
          ...(action.refund.state === 'requested'
            ? [{ value: 'approve', label: 'Approve' }]
            : []),
          { value: 'reject', label: 'Reject' },
        ],
      },
      {
        name: 'evidence_reference',
        label: 'Review evidence reference',
        kind: 'textarea',
      },
    ]
  }
  if (action?.kind === 'submit') {
    fields = [
      {
        name: 'acknowledged',
        label:
          'I will use the existing business number and original payment channel only',
        kind: 'checkbox',
      },
    ]
  }
  if (action?.kind === 'resolve') {
    fields = [
      {
        name: 'payout_paid_at',
        label: 'Actual offline payment time (successful result only)',
        kind: 'datetime',
        required: false,
      },
      {
        name: 'outcome',
        label: 'Actual payment result',
        kind: 'select',
        options: [
          { value: 'success', label: 'Confirmed successful' },
          { value: 'explicit_failed', label: 'Confirmed unpaid' },
          { value: 'unknown', label: 'Unknown; keep funds held' },
        ],
      },
      {
        name: 'original_payment_reference',
        label: 'Original provider payment reference',
        description:
          'For non-bank refunds only, independently copy the original payment reference from restricted instructions. Leave blank for bank transfers.',
        required: false,
      },
      {
        name: 'external_reference',
        label: 'External payment reference',
        required: false,
      },
      {
        name: 'evidence_reference',
        label: 'Payment evidence reference',
        kind: 'textarea',
      },
      {
        name: 'no_payment_confirmed',
        label: 'I have verified that no payment occurred (failed result only)',
        kind: 'checkbox',
      },
    ]
  }
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'A timeout is not a failed payment. Unknown results remain frozen and must be reconciled with the same business number; never submit a replacement payout.'
          )}
        </AlertDescription>
      </Alert>
      <ResourceTable resource={path} columns={columns} />
      {action && (
        <ActionForm
          title={t('Review purchase refund')}
          description={`${t('Payment business number')}: ${action.refund.business_key}`}
          fields={fields}
          onClose={() => setAction(null)}
          onSubmit={(values) => {
            const payload: OperationPayload = {
              refund_id: action.refund.id,
              version: action.refund.version,
            }
            if (action.kind === 'approve') {
              payload.decision = String(values.decision)
              payload.evidence_reference = String(values.evidence_reference)
            }
            if (action.kind === 'submit') {
              if (values.acknowledged !== true) {
                throw new Error(
                  t(
                    'Confirm the existing payment business number before continuing.'
                  )
                )
              }
              payload.business_key = action.refund.business_key
            }
            if (action.kind === 'resolve') {
              if (
                (values.outcome === 'explicit_failed') !==
                (values.no_payment_confirmed === true)
              ) {
                throw new Error(
                  t(
                    'A failed result requires verified evidence that no payment occurred.'
                  )
                )
              }
              payload.business_key = action.refund.business_key
              payload.outcome = String(values.outcome)
              if (values.outcome === 'success') {
                if (!values.payout_paid_at) {
                  throw new Error(
                    t('Enter the independently verified actual payment time.')
                  )
                }
                if (String(values.original_payment_reference ?? '').trim()) {
                  payload.original_payment_reference = String(
                    values.original_payment_reference
                  ).trim()
                }
                payload.payout_paid_at = Math.floor(
                  Date.parse(String(values.payout_paid_at)) / 1000
                )
              }
              payload.external_reference = String(values.external_reference)
              payload.evidence_reference = String(values.evidence_reference)
              payload.no_payment_confirmed =
                values.no_payment_confirmed === true
            }
            setDraft({
              action:
                action.kind === 'approve' && values.decision === 'reject'
                  ? 'purchase.refund.reject'
                  : `purchase.refund.${action.kind}`,
              request_id: requestId({ action: action.kind, ...payload }),
              payload,
              endpoint: `/finance/purchase-refunds/${action.refund.id}/${action.kind}`,
              resource: path,
              review: fields.map((field) => ({
                label: field.label,
                after: String(values[field.name]),
              })),
            })
            setAction(null)
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

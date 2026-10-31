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

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

import { useRequestId } from '../hooks/use-request-id'
import type { FundBatch, OperationDraft } from '../types'
import { ActionForm } from './action-form'
import { OperationDialog } from './operation-dialog'
import { ResourceTable } from './resource-table'
import { BusinessDate, BusinessState, Money, Quota } from './values'

export function FundBatches(props: {
  settlementEnabled: boolean
  salesEnabled: boolean
  refundEnabled: boolean
  creditSelfUseEnabled?: boolean
  selfUseEnabled: boolean
}) {
  const { t } = useTranslation()
  const requestId = useRequestId()
  const [selection, setSelection] = useState<{
    batch: FundBatch
    action: 'refund' | 'self-use'
  } | null>(null)
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  const columns: ColumnDef<FundBatch>[] = [
    { accessorKey: 'id', header: t('Batch ID'), meta: { mobileTitle: true } },
    {
      accessorKey: 'source_kind',
      header: t('Funding source'),
      cell: ({ row }) => <BusinessState value={row.original.source_kind} />,
    },
    { accessorKey: 'purchase_order_id', header: t('Procurement order') },
    {
      accessorKey: 'original_paid_minor',
      header: t('Original cost'),
      cell: ({ row }) =>
        row.original.currency ? (
          <Money
            minor={row.original.original_paid_minor}
            currency={row.original.currency}
            exponent={row.original.currency_exponent}
          />
        ) : (
          t('No cash purchase')
        ),
    },
    ...(
      [
        'original_quota',
        'available_quota',
        'reserved_quota',
        'spent_quota',
        'refund_frozen_quota',
        'refunded_quota',
      ] as const
    ).map((key) => ({
      accessorKey: key,
      header: t(
        {
          original_quota: 'Original quota',
          available_quota: 'Available quota',
          reserved_quota: 'Customer delivery reservations',
          spent_quota: 'Used quota',
          refund_frozen_quota: 'Refund frozen quota',
          refunded_quota: 'Refunded quota',
        }[key]
      ),
      cell: ({ row }: { row: { original: FundBatch } }) => (
        <Quota value={row.original[key]} />
      ),
    })),
    {
      accessorKey: 'cash_refundable',
      header: t('Cash refund eligibility'),
      cell: ({ row }) =>
        row.original.cash_refundable
          ? t('Subject to original-batch review')
          : t('Not cash refundable'),
    },
    {
      accessorKey: 'created_at',
      header: t('Created'),
      cell: ({ row }) => <BusinessDate value={row.original.created_at} />,
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => (
        <div className='flex flex-wrap gap-2'>
          <Button
            size='sm'
            variant='outline'
            disabled={
              !props.settlementEnabled ||
              !props.refundEnabled ||
              !row.original.cash_refundable ||
              row.original.available_quota <= 0
            }
            onClick={() =>
              setSelection({ batch: row.original, action: 'refund' })
            }
          >
            {t('Request purchase refund')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            disabled={
              !props.salesEnabled ||
              !props.settlementEnabled ||
              !props.selfUseEnabled ||
              (!['purchase', 'commission_transfer'].includes(
                row.original.source_kind
              ) &&
                !(
                  row.original.source_kind === 'credit' &&
                  props.creditSelfUseEnabled
                )) ||
              row.original.available_quota <= 0
            }
            onClick={() =>
              setSelection({ batch: row.original, action: 'self-use' })
            }
          >
            {t('Transfer for own use')}
          </Button>
        </div>
      ),
    },
  ]
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Paid procurement, commission conversion, legacy budgets and promotional grants keep separate provenance. Customer reservations and refund-frozen quota cannot be issued, transferred or refunded again.'
          )}
        </AlertDescription>
      </Alert>
      <ResourceTable resource='/fund-batches' columns={columns} />
      {selection && (
        <ActionForm
          title={
            selection.action === 'refund'
              ? t('Request purchase refund')
              : t('Transfer for own use')
          }
          description={
            selection.action === 'refund'
              ? t(
                  'Only eligible unused original-batch quota can be refunded. Commission conversions, free budgets and grants never become refundable principal.'
                )
              : t(
                  'Self-use is an explicit ledger transfer. Paid procurement enters paid balance; commissions and credit enter noncash reward balance. Credit self-use recognizes the original receivable.'
                )
          }
          fields={[
            {
              name: 'quota',
              label: 'Quota units',
              kind: 'integer',
              min: 1,
              max: selection.batch.available_quota,
            },
          ]}
          onClose={() => setSelection(null)}
          onSubmit={(values) => {
            const payload = {
              batch_id: selection.batch.id,
              quota: Number(values.quota),
            }
            const refund = selection.action === 'refund'
            setDraft({
              action: refund
                ? 'purchase.refund.request'
                : 'procurement.self_use',
              request_id: requestId({ action: selection.action, ...payload }),
              payload,
              endpoint: refund ? '/purchase-refunds' : '/procurement/self-use',
              resource: refund ? '/purchase-refunds' : '/fund-batches',
            })
            setSelection(null)
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

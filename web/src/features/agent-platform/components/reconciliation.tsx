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
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from '../api'
import { useRequestId } from '../hooks/use-request-id'
import type { OperationDraft, OperationPayload } from '../types'
import { ActionForm, type ActionField } from './action-form'
import { OperationDialog } from './operation-dialog'
import { ResourceTable } from './resource-table'
import { BusinessDate, BusinessState } from './values'

interface OutboxEvent {
  id: number
  reference_id: string
  kind: string
  route: string
  status: string
  attempts: number
  last_error: string
  next_attempt_at: number
}
interface CommissionRefundFact {
  id: number
  topup_reference_id: string
  refund_reference: string
  currency: string
  cumulative_refunded_minor: string
  evidence_reference: string
  created_at: number
}
interface Reconciliation {
  pending_outbox: number
  failed_outbox: number
  unknown_withdrawals: number
  paying_withdrawals: number
  invariant_violations: number[]
  accounts_checked: number
  next_account_id: number
}

export function ReconciliationPanel() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const requestId = useRequestId()
  const [after, setAfter] = useState(0)
  const [action, setAction] = useState<'refund' | 'reserve' | 'retry' | null>(
    null
  )
  const [eventId, setEventId] = useState(0)
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  const query = useQuery({
    queryKey: ['agent-platform', user?.id, 'reconciliation', after],
    queryFn: ({ signal }) =>
      getPlatform<Reconciliation>(
        '/finance/commissions/reconciliation',
        { after_id: after },
        signal
      ),
  })
  const outboxColumns: ColumnDef<OutboxEvent>[] = [
    { accessorKey: 'id', header: t('Event ID'), meta: { mobileTitle: true } },
    { accessorKey: 'reference_id', header: t('Original payment') },
    { accessorKey: 'kind', header: t('Type') },
    { accessorKey: 'route', header: t('Commission route') },
    {
      accessorKey: 'status',
      header: t('Status'),
      cell: ({ row }) => <BusinessState value={row.original.status} />,
    },
    { accessorKey: 'attempts', header: t('Processing attempts') },
    {
      accessorKey: 'last_error',
      header: t('Last processing error'),
      cell: ({ row }) => t(row.original.last_error),
    },
    {
      accessorKey: 'next_attempt_at',
      header: t('Next retry'),
      cell: ({ row }) => <BusinessDate value={row.original.next_attempt_at} />,
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) =>
        user?.role === ROLE.SUPER_ADMIN && row.original.status === 'failed' ? (
          <Button
            size='sm'
            variant='outline'
            onClick={() => {
              setEventId(row.original.id)
              setAction('retry')
            }}
          >
            {t('Review safe event retry')}
          </Button>
        ) : null,
    },
  ]
  const refundColumns: ColumnDef<CommissionRefundFact>[] = [
    {
      accessorKey: 'id',
      header: t('Refund fact ID'),
      meta: { mobileTitle: true },
    },
    { accessorKey: 'topup_reference_id', header: t('Original recharge') },
    { accessorKey: 'refund_reference', header: t('External refund reference') },
    { accessorKey: 'currency', header: t('Original currency') },
    {
      accessorKey: 'cumulative_refunded_minor',
      header: t('Cumulative refunded amount (minor units)'),
    },
    {
      accessorKey: 'evidence_reference',
      header: t('Refund evidence reference'),
    },
  ]
  let fields: ActionField[] = []
  if (action === 'refund') {
    fields = [
      { name: 'topup_reference_id', label: 'Original recharge reference' },
      { name: 'refund_reference', label: 'External refund reference' },
      {
        name: 'cumulative_refunded_minor',
        label: 'Cumulative refunded amount (minor units)',
        kind: 'minor',
      },
      {
        name: 'evidence_reference',
        label: 'Refund evidence reference',
        kind: 'textarea',
      },
    ]
  }
  if (action === 'reserve') {
    fields = [
      { name: 'user_id', label: 'User ID', kind: 'integer', min: 1 },
      { name: 'currency', label: 'Original currency' },
      {
        name: 'amount_minor',
        label: 'Reserve release amount (minor units)',
        kind: 'minor',
        min: 1,
      },
      { name: 'reason', label: 'Risk review reason', kind: 'textarea' },
    ]
  }
  if (action === 'retry') {
    fields = [
      { name: 'reason', label: 'Retry review reason', kind: 'textarea' },
    ]
  }
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertTitle>{t('Reconcile before retrying money movement')}</AlertTitle>
        <AlertDescription>
          {t(
            'Event retry never repeats a customer recharge or external payout. Refund facts record an already-confirmed refund; they do not send money. Risk reserves require a separate reviewed release.'
          )}
        </AlertDescription>
      </Alert>
      {query.isPending && <LoadingState />}
      {query.isError && (
        <ErrorState
          onRetry={() => {
            void query.refetch()
          }}
        />
      )}
      {query.data && (
        <>
          <dl className='grid grid-cols-2 gap-4 lg:grid-cols-4'>
            <div>
              <dt>{t('Pending commission events')}</dt>
              <dd>{query.data.pending_outbox}</dd>
            </div>
            <div>
              <dt>{t('Failed commission events')}</dt>
              <dd>{query.data.failed_outbox}</dd>
            </div>
            <div>
              <dt>{t('Unknown withdrawal results')}</dt>
              <dd>{query.data.unknown_withdrawals}</dd>
            </div>
            <div>
              <dt>{t('Payments in progress')}</dt>
              <dd>{query.data.paying_withdrawals}</dd>
            </div>
          </dl>
          <p role='status'>
            {t('Accounts checked in this page')}: {query.data.accounts_checked}{' '}
            · {t('Accounting discrepancies')}:{' '}
            {query.data.invariant_violations.length}
          </p>
          {query.data.invariant_violations.length > 0 && (
            <Alert variant='destructive'>
              <AlertDescription>
                {t('Review these account IDs before financial processing')}:{' '}
                {query.data.invariant_violations.join(', ')}
              </AlertDescription>
            </Alert>
          )}
          <div className='flex flex-wrap gap-2'>
            <Button
              variant='outline'
              onClick={() => {
                setAfter(0)
                void query.refetch()
              }}
            >
              {t('Restart reconciliation')}
            </Button>
            <Button
              variant='outline'
              disabled={query.data.accounts_checked < 100}
              onClick={() => setAfter(query.data.next_account_id)}
            >
              {t('Check next account page')}
            </Button>
          </div>
        </>
      )}
      <div className='flex flex-wrap gap-2'>
        <Button variant='outline' onClick={() => setAction('refund')}>
          {t('Record confirmed recharge refund')}
        </Button>
        <Button variant='outline' onClick={() => setAction('reserve')}>
          {t('Review reserve release')}
        </Button>
      </div>
      <Tabs defaultValue='outbox'>
        <TabsList aria-label={t('Reconciliation records')}>
          <TabsTrigger value='outbox'>
            {t('Commission event queue')}
          </TabsTrigger>
          <TabsTrigger value='refunds'>
            {t('Confirmed refund facts')}
          </TabsTrigger>
        </TabsList>
        <TabsContent value='outbox'>
          <ResourceTable
            resource='/finance/commissions/outbox'
            columns={outboxColumns}
          />
        </TabsContent>
        <TabsContent value='refunds'>
          <ResourceTable
            resource='/finance/commissions/refunds'
            columns={refundColumns}
          />
        </TabsContent>
      </Tabs>
      {action && (
        <ActionForm
          title={t('Review finance operation')}
          fields={fields}
          onClose={() => setAction(null)}
          onSubmit={(values) => {
            const payload: OperationPayload = { ...values }
            if (action === 'reserve') payload.user_id = Number(values.user_id)
            if (action === 'retry') payload.event_id = eventId
            const actions = {
              refund: 'commission.refund.record',
              reserve: 'commission.reserve.release',
              retry: 'commission.outbox.retry',
            }
            const endpoints = {
              refund: '/finance/commissions/refunds',
              reserve: '/finance/commissions/reserve-release',
              retry: '/finance/commissions/outbox/retry',
            }
            setDraft({
              action: actions[action],
              request_id: requestId({ action, ...payload }),
              payload,
              endpoint: endpoints[action],
              resource: '/finance/commissions/refunds',
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

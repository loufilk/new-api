/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { formatTimestampToDate } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { postPlatform } from '../api'
import { useRequestId } from '../hooks/use-request-id'
import { formatAgentQuota } from '../lib/money'
import { getSimplePaymentOrders, type SimplePaymentOrder } from '../simple-api'
import { ActionForm } from './action-form'
import { SimpleRecordPager } from './simple-record-pager'

export function SimplePaymentRecords(props: { agentUserId?: number }) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: [
      'agent-platform',
      'simple-payment-orders',
      props.agentUserId,
      page,
    ],
    queryFn: () =>
      getSimplePaymentOrders({
        agent_user_id: props.agentUserId,
        p: page,
        page_size: 20,
      }),
  })
  if (query.isPending) return <LoadingState />
  if (query.isError) {
    return (
      <ErrorState
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }
  return (
    <div className='space-y-3'>
      <div className='flex items-center justify-between gap-2'>
        <h2 className='text-sm font-semibold'>
          {t('Customer payment records')}
        </h2>
        <span className='text-muted-foreground text-xs'>
          {t('{{count}} records', { count: query.data.total })}
        </span>
      </div>
      {query.data.items.length ? (
        <div className='divide-border overflow-hidden rounded-lg border'>
          {query.data.items.map((row) => (
            <SimplePaymentRow key={row.id} row={row} />
          ))}
        </div>
      ) : (
        <EmptyState
          title={t('No customer payment records')}
          description={t('Customer payment links will appear here.')}
        />
      )}
      <SimpleRecordPager
        page={page}
        total={query.data.total}
        onChange={setPage}
      />
    </div>
  )
}

function SimplePaymentRow(props: { row: SimplePaymentOrder }) {
  const { t } = useTranslation()
  const [refundOpen, setRefundOpen] = useState(false)
  const [revealOpen, setRevealOpen] = useState(false)
  const [revealedKey, setRevealedKey] = useState('')
  const identify = useRequestId()
  const client = useQueryClient()
  const root = useAuthStore(
    (state) => state.auth.user?.role === ROLE.SUPER_ADMIN
  )
  const row = props.row
  const status =
    {
      pending: t('Awaiting payment'),
      paid: t('Paid'),
      redeemed: t('Redeemed'),
      expired: t('Expired'),
      closed: t('Closed'),
      refunded: t('Refunded'),
    }[row.status] ?? row.status
  let profitStatus = t('Awaiting payment')
  if (row.status === 'refunded') profitStatus = t('Reward cancelled')
  else if (row.profit_matured) profitStatus = t('Profit released')
  else if (row.paid_at > 0) profitStatus = t('Profit frozen')
  return (
    <>
      <div className='flex flex-wrap items-start gap-3 border-b p-3 last:border-b-0'>
        <div className='min-w-0 flex-1 basis-40'>
          <div className='font-medium break-words'>{row.name}</div>
          <div className='text-muted-foreground text-xs'>
            {status} · {formatTimestampToDate(row.created_at)} · {t('Agent')} #
            {row.agent_user_id}
          </div>
        </div>
        <dl className='grid grid-cols-2 gap-x-4 gap-y-1 text-xs sm:grid-cols-3'>
          <div>
            <dt className='text-muted-foreground'>{t('Quota')}</dt>
            <dd>{formatAgentQuota(row.quota)}</dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>
              {t('Customer price (yuan)')}
            </dt>
            <dd>¥{(row.sale_price_fen / 100).toFixed(2)}</dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>{t('Code cost')}</dt>
            <dd>¥{(row.cost_price_fen / 100).toFixed(2)}</dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>
              {t('Payment channel fee')}
            </dt>
            <dd>¥{(row.fee_fen / 100).toFixed(2)}</dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>
              {t('Payment income available')}
            </dt>
            <dd>
              {formatAgentQuota(row.profit_quota)} · {profitStatus}
            </dd>
          </div>
          {row.paid_at > 0 &&
            !row.profit_matured &&
            row.status !== 'refunded' && (
              <div>
                <dt className='text-muted-foreground'>
                  {t('Expected arrival')}
                </dt>
                <dd>{formatTimestampToDate(row.matures_at)}</dd>
              </div>
            )}
        </dl>
        {row.customer_url && row.status === 'pending' && (
          <div className='flex w-full min-w-0 items-center gap-1'>
            <code className='min-w-0 flex-1 truncate text-xs'>
              {row.customer_url}
            </code>
            <CopyButton
              value={row.customer_url}
              aria-label={t('Copy payment link')}
            />
          </div>
        )}
        {(row.redemption_key || revealedKey) && (
          <div className='flex w-full min-w-0 items-center gap-1'>
            <code className='min-w-0 flex-1 text-xs break-all'>
              {row.redemption_key || revealedKey}
            </code>
            <CopyButton
              value={row.redemption_key || revealedKey}
              aria-label={t('Copy redemption code')}
            />
          </div>
        )}
        {root && row.redemption_id > 0 && (
          <Button
            size='sm'
            variant='outline'
            onClick={() => setRevealOpen(true)}
          >
            {t('View full code')}
          </Button>
        )}
        {root && row.refund_evidence && (
          <p className='w-full text-xs break-words'>
            {t('Support evidence')}: {row.refund_evidence} · {t('Operator')}:{' '}
            {row.refunded_by} · {formatTimestampToDate(row.refunded_at ?? 0)}
          </p>
        )}
        {root && row.status === 'paid' && (
          <Button
            size='sm'
            variant='outline'
            onClick={() => setRefundOpen(true)}
          >
            {t('Record customer refund')}
          </Button>
        )}
      </div>
      {revealOpen && (
        <ActionForm
          title={t('View full code')}
          fields={[
            { name: 'reason', label: 'Reason', kind: 'textarea', max: 255 },
          ]}
          onClose={() => setRevealOpen(false)}
          onSubmit={async (values) => {
            const result = await postPlatform<{ key: string }>(
              `/simple/finance/codes/${row.redemption_id}/reveal`,
              { reason: String(values.reason) }
            )
            setRevealedKey(result.key)
            setRevealOpen(false)
          }}
          submitLabel={t('View full code')}
        />
      )}
      {refundOpen && (
        <ActionForm
          title={t('Record customer refund')}
          description={t(
            'Record only a completed external refund. This also cancels the unredeemed code and reverses its income.'
          )}
          fields={[
            { name: 'reason', label: 'Reason', kind: 'textarea', max: 255 },
            {
              name: 'evidence',
              label: 'Support evidence',
              kind: 'textarea',
              max: 255,
            },
            {
              name: 'confirmed',
              label: 'I confirm this change and its financial impact',
              kind: 'checkbox',
              mustBeTrue: true,
            },
          ]}
          onClose={() => setRefundOpen(false)}
          onSubmit={async (values) => {
            const payload = {
              order_id: row.id,
              reason: String(values.reason),
              evidence: String(values.evidence),
            }
            await postPlatform('/simple/payment/refunds', {
              request_id: identify(payload),
              ...payload,
            })
            await client.invalidateQueries({ queryKey: ['agent-platform'] })
            await client.invalidateQueries({ queryKey: ['simple-root'] })
            setRefundOpen(false)
          }}
          submitLabel={t('Record customer refund')}
        >
          <p className='mb-3 text-sm'>
            {t('Refund amount')}: ¥{(row.sale_price_fen / 100).toFixed(2)}
          </p>
        </ActionForm>
      )}
    </>
  )
}

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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { TFunction } from 'i18next'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  getSupportEmailOutbox,
  updateSupportEmailOutbox,
  type SupportEmailOutboxItem,
} from './api'

type QueueAction = { id: number; action: 'retry' | 'cancel' }

function queueStatus(t: TFunction, status: string): string {
  switch (status) {
    case 'pending':
      return t('Pending')
    case 'retry':
      return t('Retrying')
    case 'leased':
      return t('Processing')
    case 'sending':
      return t('Sending')
    case 'sent':
      return t('Sent')
    case 'cancelled':
      return t('Cancelled')
    case 'dead':
      return t('Delivery failed')
    case 'coalesced':
      return t('Combined notification')
    default:
      return t('Unknown')
  }
}

function queueEvent(t: TFunction, event: string): string {
  switch (event) {
    case 'created':
      return t('New ticket')
    case 'user_reply':
      return t('Customer reply')
    case 'admin_reply':
      return t('Support reply')
    default:
      return t('Unknown')
  }
}

function canCancel(status: string): boolean {
  return ['pending', 'retry', 'dead'].includes(status)
}

export function SupportEmailOutbox(props: { userId: number }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState<QueueAction | null>(null)
  const queryKey = ['support-email-outbox', props.userId] as const
  const query = useQuery({
    queryKey: [...queryKey, page],
    queryFn: ({ signal }) => getSupportEmailOutbox(page, signal),
    retry: false,
    staleTime: 0,
    refetchOnMount: 'always',
    meta: { errorToast: false },
  })
  const mutation = useMutation({
    mutationFn: updateSupportEmailOutbox,
    onSuccess: async () => {
      setSelected(null)
      await client.invalidateQueries({ queryKey })
    },
    onError: (error) => handleServerError(error),
  })
  const columns: StaticDataTableColumn<SupportEmailOutboxItem>[] = [
    {
      id: 'ticket',
      header: t('Ticket'),
      cell: (row) => t('Ticket #{{id}}', { id: row.ticket_id }),
    },
    {
      id: 'event',
      header: t('Event'),
      cell: (row) => queueEvent(t, row.event),
    },
    {
      id: 'status',
      header: t('Status'),
      cell: (row) => (
        <Badge variant='outline'>{queueStatus(t, row.status)}</Badge>
      ),
    },
    { id: 'attempts', header: t('Attempts'), cell: (row) => row.attempts },
    {
      id: 'next',
      header: t('Next attempt'),
      cell: (row) => formatTimestamp(row.next_attempt_at),
    },
    {
      id: 'error',
      header: t('Error code'),
      cell: (row) => row.last_error_code || '—',
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: (row) => (
        <div className='flex gap-2'>
          {row.status === 'dead' && (
            <Button
              size='sm'
              variant='outline'
              disabled={mutation.isPending || query.isFetching}
              aria-label={t('Retry notification #{{id}}', { id: row.id })}
              onClick={() => setSelected({ id: row.id, action: 'retry' })}
            >
              {t('Retry')}
            </Button>
          )}
          {canCancel(row.status) && (
            <Button
              size='sm'
              variant='outline'
              disabled={mutation.isPending || query.isFetching}
              aria-label={t('Cancel notification #{{id}}', { id: row.id })}
              onClick={() => setSelected({ id: row.id, action: 'cancel' })}
            >
              {t('Cancel')}
            </Button>
          )}
        </div>
      ),
    },
  ]
  const totalPages = Math.max(1, Math.ceil((query.data?.total ?? 0) / 20))
  const selectedRow = query.data?.items.find((row) => row.id === selected?.id)
  const selectedAllowed =
    selectedRow &&
    (selected?.action === 'retry'
      ? selectedRow.status === 'dead'
      : canCancel(selectedRow.status))

  let content
  if (query.isError) {
    content = (
      <ErrorState
        title={t('Failed to load notification queue')}
        onRetry={() => void query.refetch()}
      />
    )
  } else if (!query.isFetchedAfterMount || !query.data) {
    content = <LoadingState />
  } else {
    content = (
      <>
        <StaticDataTable
          columns={columns}
          data={query.data.items}
          getRowKey={(row) => row.id}
          emptyContent={t('No notifications found')}
          tableProps={{ 'aria-label': t('Support notification queue') }}
        />
        {totalPages > 1 && (
          <div className='flex items-center justify-between gap-2'>
            <Button
              variant='outline'
              disabled={page <= 1 || mutation.isPending}
              onClick={() => setPage((value) => Math.max(1, value - 1))}
            >
              {t('Previous')}
            </Button>
            <span className='text-muted-foreground text-sm'>
              {t('Page {{page}} of {{total}}', { page, total: totalPages })}
            </span>
            <Button
              variant='outline'
              disabled={page >= totalPages || mutation.isPending}
              onClick={() => setPage((value) => value + 1)}
            >
              {t('Next')}
            </Button>
          </div>
        )}
      </>
    )
  }
  return (
    <section
      aria-label={t('Support notification queue')}
      className='flex min-w-0 flex-col gap-3'
    >
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <h4 className='font-medium'>{t('Support notification queue')}</h4>
        <Button
          variant='outline'
          disabled={query.isFetching || mutation.isPending}
          onClick={() => void query.refetch()}
        >
          {t('Refresh')}
        </Button>
      </div>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Repeated events may be combined. SMTP acknowledgement loss can still cause a duplicate; retries do not guarantee exactly-once delivery.'
        )}
      </p>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Mail already approved for dispatch may still be delivered and cannot be recalled.'
        )}
      </p>
      {content}
      <ConfirmDialog
        open={selected !== null}
        onOpenChange={(open) => {
          if (!open && !mutation.isPending) setSelected(null)
        }}
        title={
          selected?.action === 'retry'
            ? t('Retry email delivery?')
            : t('Cancel queued notification?')
        }
        desc={
          selected?.action === 'retry'
            ? t(
                'This requeues one failed notification and may send an email. The current settings and verified recipient are checked again.'
              )
            : t(
                'This notification will not be retried. Mail already dispatched cannot be recalled.'
              )
        }
        confirmText={selected?.action === 'retry' ? t('Retry') : t('Confirm')}
        destructive={selected?.action === 'cancel'}
        isLoading={mutation.isPending}
        disabled={!selectedAllowed || query.isError || query.isFetching}
        handleConfirm={() => {
          if (selected && selectedAllowed && !mutation.isPending) {
            mutation.mutate(selected)
          }
        }}
      />
    </section>
  )
}

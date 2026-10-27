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
import { Inbox, LifeBuoy, Plus, Search } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { useIsAdmin } from '@/hooks/use-admin'
import { handleServerError } from '@/lib/handle-server-error'

import {
  createSupportTicket,
  getSupportTicket,
  listSupportTickets,
  markSupportTicketRead,
  replySupportTicket,
  updateSupportTicketStatus,
} from './api'
import { CreateTicketDialog } from './components/create-ticket-dialog'
import { TicketDetail } from './components/ticket-detail'
import type { CreateSupportTicketValues } from './lib/schema'
import {
  supportTicketStatusLabel,
  supportTicketStatusVariant,
} from './lib/status'
import type { SupportTicket, SupportTicketStatus } from './types'

const PAGE_SIZE = 20

export function SupportCenter() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const admin = useIsAdmin()
  const [searchInput, setSearchInput] = useState('')
  const [keyword, setKeyword] = useState('')
  const [statusFilter, setStatusFilter] = useState<'all' | SupportTicketStatus>(
    'all'
  )
  const [unreadOnly, setUnreadOnly] = useState(false)
  const [page, setPage] = useState(1)
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [createOpen, setCreateOpen] = useState(false)

  const listQueryKey = [
    'support-tickets',
    admin,
    keyword,
    statusFilter,
    unreadOnly,
    page,
  ] as const
  const listQuery = useQuery({
    queryKey: listQueryKey,
    queryFn: () =>
      listSupportTickets({
        admin,
        keyword,
        status: statusFilter === 'all' ? undefined : statusFilter,
        unreadOnly,
        page,
        pageSize: PAGE_SIZE,
      }),
  })
  const detailQuery = useQuery({
    queryKey: ['support-ticket', admin, selectedId],
    queryFn: () => getSupportTicket(admin, selectedId as number),
    enabled: selectedId != null,
  })

  const refreshTickets = async (ticketId?: number) => {
    const tasks: Promise<unknown>[] = [
      queryClient.invalidateQueries({ queryKey: ['support-tickets'] }),
    ]
    if (ticketId != null) {
      tasks.push(
        queryClient.invalidateQueries({
          queryKey: ['support-ticket', admin, ticketId],
        })
      )
    }
    await Promise.all(tasks)
  }

  const createMutation = useMutation({
    mutationFn: createSupportTicket,
    onSuccess: async (detail) => {
      setCreateOpen(false)
      setSelectedId(detail.ticket.id)
      toast.success(t('Ticket created'))
      await refreshTickets(detail.ticket.id)
    },
    onError: (error) => handleServerError(error),
  })
  const replyMutation = useMutation({
    mutationFn: replySupportTicket,
    onSuccess: async (ticket) => {
      toast.success(t('Reply sent'))
      await refreshTickets(ticket.id)
    },
    onError: (error) => handleServerError(error),
  })
  const statusMutation = useMutation({
    mutationFn: updateSupportTicketStatus,
    onSuccess: async (ticket) => {
      toast.success(t('Ticket status updated'))
      await refreshTickets(ticket.id)
    },
    onError: (error) => handleServerError(error),
  })
  const readMutation = useMutation({
    mutationFn: markSupportTicketRead,
    onSuccess: async (_, input) => {
      await refreshTickets(input.ticketId)
    },
    onError: (error) => handleServerError(error),
  })

  const handleTicketSelect = (ticket: SupportTicket) => {
    setSelectedId(ticket.id)
    const unread = admin ? ticket.admin_unread : ticket.user_unread
    if (unread && !readMutation.isPending) {
      readMutation.mutate({ admin, ticketId: ticket.id })
    }
  }
  const totalPages = Math.max(
    1,
    Math.ceil((listQuery.data?.total ?? 0) / PAGE_SIZE)
  )
  const counts = listQuery.data?.status_counts

  let detailContent: ReactNode
  if (selectedId == null) {
    detailContent = (
      <EmptyState
        icon={LifeBuoy}
        title={t('Select a ticket')}
        description={t('Choose a ticket to view the conversation.')}
        bordered
      />
    )
  } else if (detailQuery.isLoading) {
    detailContent = <LoadingState message={t('Loading conversation...')} />
  } else if (detailQuery.isError || !detailQuery.data) {
    detailContent = (
      <ErrorState
        description={t('Failed to load ticket conversation')}
        onRetry={() => void detailQuery.refetch()}
      />
    )
  } else {
    detailContent = (
      <TicketDetail
        admin={admin}
        detail={detailQuery.data}
        replyPending={replyMutation.isPending}
        statusPending={statusMutation.isPending}
        onReply={(message) =>
          replyMutation
            .mutateAsync({ admin, ticketId: selectedId, message })
            .then(() => undefined)
        }
        onStatusChange={(status) =>
          statusMutation
            .mutateAsync({ ticketId: selectedId, status })
            .then(() => undefined)
        }
      />
    )
  }

  const hasNoTickets = listQuery.data?.items.length === 0

  let ticketContent: ReactNode
  if (listQuery.isLoading) {
    ticketContent = <LoadingState message={t('Loading tickets...')} />
  } else if (listQuery.isError) {
    ticketContent = (
      <ErrorState
        description={t('Failed to load tickets')}
        onRetry={() => void listQuery.refetch()}
      />
    )
  } else if (hasNoTickets && selectedId == null) {
    const hasFilters = keyword || statusFilter !== 'all' || unreadOnly
    ticketContent = (
      <EmptyState
        icon={Inbox}
        title={t('No tickets found')}
        description={
          hasFilters
            ? t('No tickets match the current filters.')
            : t(
                'When you need help, create a ticket and continue the conversation here.'
              )
        }
        action={
          !admin && !keyword && statusFilter === 'all' && !unreadOnly ? (
            <Button onClick={() => setCreateOpen(true)}>
              <Plus aria-hidden='true' />
              {t('New ticket')}
            </Button>
          ) : undefined
        }
        bordered
      />
    )
  } else {
    ticketContent = (
      <div className='grid min-h-0 gap-4 lg:grid-cols-[minmax(280px,0.85fr)_minmax(0,1.6fr)]'>
        <div className='flex min-h-0 flex-col gap-2'>
          {listQuery.data?.items.map((ticket) => (
            <Button
              key={ticket.id}
              variant={selectedId === ticket.id ? 'secondary' : 'outline'}
              className='h-auto w-full justify-start px-3 py-3 text-left whitespace-normal'
              onClick={() => handleTicketSelect(ticket)}
            >
              <div className='min-w-0 flex-1'>
                <div className='flex items-start justify-between gap-2'>
                  <span className='line-clamp-2 font-medium'>
                    {ticket.subject}
                  </span>
                  {(admin ? ticket.admin_unread : ticket.user_unread) && (
                    <span
                      className='bg-primary mt-1 size-2 shrink-0 rounded-full'
                      aria-label={t('Unread')}
                    />
                  )}
                </div>
                <div className='mt-2 flex flex-wrap items-center gap-2'>
                  <Badge variant={supportTicketStatusVariant(ticket.status)}>
                    {supportTicketStatusLabel(t, ticket.status)}
                  </Badge>
                  <span className='text-muted-foreground text-xs'>
                    {t('Ticket #{{id}}', { id: ticket.id })}
                  </span>
                  {admin && (
                    <span className='text-muted-foreground text-xs'>
                      {t('User #{{id}}', { id: ticket.user_id })}
                    </span>
                  )}
                </div>
              </div>
            </Button>
          ))}
          {totalPages > 1 && (
            <div className='mt-1 flex items-center justify-between gap-2'>
              <Button
                variant='outline'
                size='sm'
                disabled={page <= 1}
                onClick={() => setPage((current) => Math.max(1, current - 1))}
              >
                {t('Previous')}
              </Button>
              <span className='text-muted-foreground text-xs'>
                {t('Page {{page}} of {{total}}', { page, total: totalPages })}
              </span>
              <Button
                variant='outline'
                size='sm'
                disabled={page >= totalPages}
                onClick={() =>
                  setPage((current) => Math.min(totalPages, current + 1))
                }
              >
                {t('Next')}
              </Button>
            </div>
          )}
        </div>
        {detailContent}
      </div>
    )
  }

  return (
    <>
      <SectionPageLayout stackActionsOnMobile>
        <SectionPageLayout.Title>{t('Support Center')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          {!admin && (
            <Button onClick={() => setCreateOpen(true)}>
              <Plus aria-hidden='true' />
              {t('New ticket')}
            </Button>
          )}
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-7xl flex-col gap-4'>
            <div className='grid gap-3 sm:grid-cols-3'>
              <StatusCounter
                label={t('Platform processing')}
                count={counts?.processing ?? 0}
                loading={listQuery.isLoading}
              />
              <StatusCounter
                label={t('Waiting for your reply')}
                count={counts?.awaiting_user ?? 0}
                loading={listQuery.isLoading}
              />
              <StatusCounter
                label={t('Resolved')}
                count={counts?.resolved ?? 0}
                loading={listQuery.isLoading}
              />
            </div>

            <Card>
              <CardContent className='flex flex-col gap-3 sm:flex-row sm:items-center'>
                <form
                  className='flex min-w-0 flex-1 gap-2'
                  onSubmit={(event) => {
                    event.preventDefault()
                    setPage(1)
                    setKeyword(searchInput.trim())
                  }}
                >
                  <Input
                    value={searchInput}
                    onChange={(event) => setSearchInput(event.target.value)}
                    placeholder={t('Search tickets')}
                    aria-label={t('Search tickets')}
                  />
                  <Button
                    type='submit'
                    variant='outline'
                    aria-label={t('Search')}
                  >
                    <Search aria-hidden='true' />
                  </Button>
                </form>
                <Select
                  value={statusFilter}
                  onValueChange={(value) => {
                    if (typeof value !== 'string') return
                    setPage(1)
                    setStatusFilter(value as 'all' | SupportTicketStatus)
                  }}
                >
                  <SelectTrigger
                    className='w-full sm:w-48'
                    aria-label={t('Status')}
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectItem value='all'>{t('All statuses')}</SelectItem>
                    <SelectItem value='processing'>
                      {t('Platform processing')}
                    </SelectItem>
                    <SelectItem value='awaiting_user'>
                      {t('Waiting for your reply')}
                    </SelectItem>
                    <SelectItem value='resolved'>{t('Resolved')}</SelectItem>
                  </SelectContent>
                </Select>
                <div className='flex shrink-0 items-center gap-2 rounded-lg border px-3 py-2'>
                  <Switch
                    id='support-unread-only'
                    checked={unreadOnly}
                    onCheckedChange={(checked) => {
                      setPage(1)
                      setUnreadOnly(checked)
                    }}
                  />
                  <Label
                    htmlFor='support-unread-only'
                    className='whitespace-nowrap'
                  >
                    {t('Unread only')}
                  </Label>
                </div>
              </CardContent>
            </Card>

            {ticketContent}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <CreateTicketDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        pending={createMutation.isPending}
        onSubmit={(values: CreateSupportTicketValues) =>
          createMutation.mutateAsync(values).then(() => undefined)
        }
      />
    </>
  )
}

function StatusCounter(props: {
  label: string
  count: number
  loading: boolean
}) {
  return (
    <Card size='sm'>
      <CardContent className='flex items-center justify-between gap-3'>
        <span className='text-muted-foreground text-sm'>{props.label}</span>
        <span className='text-lg font-semibold tabular-nums'>
          {props.loading ? '—' : props.count}
        </span>
      </CardContent>
    </Card>
  )
}

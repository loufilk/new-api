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
import {
  focusManager,
  onlineManager,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { Inbox, LifeBuoy, Plus, RefreshCw, Search } from 'lucide-react'
import { useCallback, useRef, useState, type ReactNode } from 'react'
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
import { getAuthSessionEpoch } from '@/lib/auth-session'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorStatus } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  createSupportTicket,
  getSupportTicket,
  listSupportTickets,
  markSupportTicketRead,
  replySupportTicket,
  SupportTicketQueryRejected,
  updateSupportTicketStatus,
} from './api'
import { CreateTicketDialog } from './components/create-ticket-dialog'
import { TicketDetail } from './components/ticket-detail'
import { SupportEmailPreferences } from './email/preferences'
import { supportTicketCategoryLabel } from './lib/categories'
import {
  supportTicketStatusLabel,
  supportTicketStatusVariant,
} from './lib/status'
import type { SupportTicketListData, SupportTicketStatus } from './types'

const PAGE_SIZE = 20
const TICKET_LIST_POLL_MS = 60_000
const TICKET_DETAIL_POLL_MS = 20_000

function cannotShowCachedTicketData(error: unknown): boolean {
  const status = getServerErrorStatus(error)
  return (
    error instanceof SupportTicketQueryRejected ||
    status === 401 ||
    status === 403 ||
    status === 404
  )
}

// Background checks must not redirect the page or repeatedly show toasts.
// The page still offers its own error states and manual Refresh.
const supportRefreshOptions = {
  refetchOnWindowFocus: 'always',
  refetchIntervalInBackground: false,
  retry: false,
  meta: { errorToast: false, redirectOnInternalServerError: false },
} as const

export function SupportCenter(
  props: { ticketId?: number; onTicketSelect?: (ticketId: number) => void } = {}
) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const admin = useIsAdmin()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const [searchInput, setSearchInput] = useState('')
  const [keyword, setKeyword] = useState('')
  const [statusFilter, setStatusFilter] = useState<'all' | SupportTicketStatus>(
    'all'
  )
  const [unreadOnly, setUnreadOnly] = useState(false)
  const [page, setPage] = useState(1)
  const [localSelection, setLocalSelection] = useState(props.ticketId ?? null)
  const selectedId = props.onTicketSelect
    ? (props.ticketId ?? null)
    : localSelection
  const selectTicket = (ticketId: number) => {
    if (props.onTicketSelect) props.onTicketSelect(ticketId)
    else setLocalSelection(ticketId)
  }
  const [createOpen, setCreateOpen] = useState(false)

  const listQueryKey = [
    'support-tickets',
    userId,
    admin,
    keyword,
    statusFilter,
    unreadOnly,
    page,
    sessionId,
  ] as const
  const listQuery = useQuery<SupportTicketListData>({
    ...supportRefreshOptions,
    enabled: userId != null,
    refetchInterval: (query) =>
      cannotShowCachedTicketData(query.state.error)
        ? false
        : TICKET_LIST_POLL_MS,
    queryKey: listQueryKey,
    queryFn: ({ signal }) =>
      listSupportTickets({
        signal,
        admin,
        keyword,
        status: statusFilter === 'all' ? undefined : statusFilter,
        unreadOnly,
        page,
        pageSize: PAGE_SIZE,
      }),
  })
  const detailQuery = useQuery({
    ...supportRefreshOptions,
    queryKey: ['support-ticket', userId, admin, selectedId, sessionId],
    queryFn: ({ signal }) =>
      getSupportTicket(admin, selectedId as number, signal),
    enabled: userId != null && selectedId != null,
    refetchInterval: (query) =>
      cannotShowCachedTicketData(query.state.error)
        ? false
        : TICKET_DETAIL_POLL_MS,
  })

  const refreshTickets = async (ticketId?: number) => {
    const tasks: Promise<unknown>[] = [
      queryClient.invalidateQueries({
        queryKey: ['support-tickets', userId, admin],
      }),
    ]
    if (ticketId != null) {
      tasks.push(
        queryClient.invalidateQueries({
          queryKey: ['support-ticket', userId, admin, ticketId],
        })
      )
    }
    await Promise.all(tasks)
  }

  const createMutation = useMutation({
    mutationFn: createSupportTicket,
    onSuccess: async (detail) => {
      setCreateOpen(false)
      selectTicket(detail.ticket.id)
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
  // Never treat dispatch as proof the server marked the conversation read.
  const lastAcknowledgment = useRef<string | null>(null)
  const lastReportedReadError = useRef<string | null>(null)
  const pendingReads = useRef(new Set<string>())
  const lastReadAttempt = useRef<{
    snapshot: string
    observedAt: number
    startedAt: number
  } | null>(null)
  const { mutate: acknowledgeRead } = useMutation({
    // An automatic acknowledgement should never be queued for later replay.
    networkMode: 'always',
    retry: false,
    mutationFn: async (
      input: Parameters<typeof markSupportTicketRead>[0] & {
        userId: number
        sessionEpoch: number
        scope: string
        snapshot: string
      }
    ) => {
      if (getAuthSessionEpoch() !== input.sessionEpoch) return
      return markSupportTicketRead(input)
    },
    onSuccess: async (result, input) => {
      pendingReads.current.delete(input.scope)
      if (!result || getAuthSessionEpoch() !== input.sessionEpoch) return
      if (result.acknowledged === true) {
        lastAcknowledgment.current = input.snapshot
      }
      const refetchType = focusManager.isFocused() ? 'active' : 'none'
      const tasks = [
        queryClient.invalidateQueries({
          queryKey: [
            'support-ticket',
            input.userId,
            input.admin,
            input.ticketId,
          ],
          refetchType,
        }),
      ]
      if (result.acknowledged) {
        tasks.push(
          queryClient.invalidateQueries({
            queryKey: ['support-tickets', input.userId, input.admin],
            refetchType,
          })
        )
      }
      await Promise.all(tasks)
    },
    onError: (error, input) => {
      pendingReads.current.delete(input.scope)
      // A lost response may already have cleared unread. No blind write:
      // the next successful detail read must still show this snapshot unread.
      if (
        getAuthSessionEpoch() === input.sessionEpoch &&
        lastReportedReadError.current !== input.snapshot
      ) {
        lastReportedReadError.current = input.snapshot
        handleServerError(error)
      }
    },
  })

  const detail = detailQuery.data
  const observedAt = detailQuery.dataUpdatedAt
  const detailReady = detailQuery.isSuccess && !detailQuery.isFetching
  const onLatestSeen = useCallback(() => {
    if (
      !detailReady ||
      !focusManager.isFocused() ||
      !onlineManager.isOnline() ||
      !detail ||
      userId == null
    ) {
      return
    }
    const { ticket, messages } = detail
    const latest = messages.at(-1)
    const unread = admin ? ticket.admin_unread : ticket.user_unread
    if (ticket.id !== selectedId || !unread || !latest) return
    // Only acknowledge the snapshot that was actually displayed. The server
    // rejects it if the ticket changes before this request reaches it.
    const auth = useAuthStore.getState().auth
    if (auth.user?.id !== userId || auth.session?.sid !== sessionId) return
    const sessionEpoch = getAuthSessionEpoch()
    const scope = `${sessionEpoch}:${userId}:${admin}:${ticket.id}`
    const snapshot = `${scope}:${latest.id}:${ticket.status}:${ticket.updated_at}`
    const last = lastReadAttempt.current
    if (
      lastAcknowledgment.current === snapshot ||
      pendingReads.current.has(scope) ||
      (last?.snapshot === snapshot &&
        (last.observedAt === observedAt ||
          Date.now() - last.startedAt < TICKET_DETAIL_POLL_MS / 2))
    ) {
      return
    }
    pendingReads.current.add(scope)
    lastReadAttempt.current = { snapshot, observedAt, startedAt: Date.now() }
    acknowledgeRead({
      userId,
      sessionEpoch,
      admin,
      ticketId: ticket.id,
      scope,
      snapshot,
      throughMessageId: latest.id,
      observedStatus: ticket.status,
    })
  }, [
    acknowledgeRead,
    admin,
    detail,
    detailReady,
    observedAt,
    selectedId,
    sessionId,
    userId,
  ])

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
  } else if (!detail || cannotShowCachedTicketData(detailQuery.error)) {
    detailContent = (
      <ErrorState
        description={t('Failed to load ticket conversation')}
        onRetry={() => void detailQuery.refetch()}
      />
    )
  } else {
    detailContent = (
      <TicketDetail
        key={`${userId}:${admin}:${detail.ticket.id}`}
        admin={admin}
        detail={detail}
        onLatestSeen={onLatestSeen}
        replyPending={replyMutation.isPending}
        statusPending={statusMutation.isPending}
        onReply={(submission) =>
          replyMutation
            .mutateAsync({ admin, ticketId: selectedId, ...submission })
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

  let listContent: ReactNode
  if (listQuery.isLoading) {
    listContent = <LoadingState message={t('Loading tickets...')} />
  } else if (!listQuery.data || cannotShowCachedTicketData(listQuery.error)) {
    listContent = (
      <ErrorState
        description={t('Failed to load tickets')}
        onRetry={() => void listQuery.refetch()}
      />
    )
  } else if (hasNoTickets && page === 1) {
    const hasFilters = keyword || statusFilter !== 'all' || unreadOnly
    listContent = (
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
    listContent = (
      <div className='flex min-h-0 flex-col gap-2'>
        {listQuery.data?.items.map((ticket) => (
          <Button
            key={ticket.id}
            variant={selectedId === ticket.id ? 'secondary' : 'outline'}
            className='h-auto w-full justify-start px-3 py-3 text-left whitespace-normal'
            onClick={() => selectTicket(ticket.id)}
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
                  {supportTicketStatusLabel(t, ticket.status, admin)}
                </Badge>
                <span className='text-muted-foreground text-xs'>
                  {t('Ticket #{{id}}', { id: ticket.id })}
                  {' · '}
                  {supportTicketCategoryLabel(t, ticket.category)}
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
        {(totalPages > 1 || page > 1) && (
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
    )
  }

  const showDetailPane =
    selectedId != null ||
    (!hasNoTickets &&
      !!listQuery.data &&
      !cannotShowCachedTicketData(listQuery.error))

  return (
    <>
      <SectionPageLayout stackActionsOnMobile>
        <SectionPageLayout.Title>{t('Support Center')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button
            type='button'
            variant='outline'
            disabled={
              userId == null || listQuery.isFetching || detailQuery.isFetching
            }
            onClick={() => {
              void listQuery.refetch()
              if (selectedId != null) void detailQuery.refetch()
            }}
          >
            <RefreshCw aria-hidden='true' />
            {t('Refresh')}
          </Button>
          {!admin && (
            <Button onClick={() => setCreateOpen(true)}>
              <Plus aria-hidden='true' />
              {t('New ticket')}
            </Button>
          )}
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-7xl flex-col gap-4'>
            {!admin && <SupportEmailPreferences />}
            <div className='grid gap-3 sm:grid-cols-3'>
              <StatusCounter
                label={t('Platform processing')}
                count={counts?.processing ?? 0}
                loading={listQuery.isLoading}
              />
              <StatusCounter
                label={supportTicketStatusLabel(t, 'awaiting_user', admin)}
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
                      {supportTicketStatusLabel(t, 'awaiting_user', admin)}
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

            {showDetailPane ? (
              <div className='grid min-h-0 gap-4 lg:grid-cols-[minmax(280px,0.85fr)_minmax(0,1.6fr)]'>
                {listContent}
                {detailContent}
              </div>
            ) : (
              listContent
            )}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <CreateTicketDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        pending={createMutation.isPending}
        onSubmit={(values) =>
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

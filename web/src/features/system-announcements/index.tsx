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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Bell,
  CheckCheck,
  Clock3,
  Megaphone,
  Pin,
  RefreshCw,
  Search,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Main } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { RichContent } from '@/components/rich-content'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { AnnouncementDetailModal } from '@/features/dashboard/components/overview/announcement-detail-dialog'
import { getPreviewText } from '@/features/dashboard/lib'
import { getAnnouncementKey } from '@/hooks/use-notifications'
import { toIntlLocale } from '@/i18n/languages'
import { getNotice } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'
import { statusQueryOptions } from '@/lib/status-query'
import { formatDateTimeObject } from '@/lib/time'
import { useAuthStore } from '@/stores/auth-store'
import {
  EMPTY_NOTIFICATION_KEYS,
  notificationScope,
  useNotificationStore,
} from '@/stores/notification-store'

import { publishedAnnouncementItems, type Announcement } from './announcements'

export function SystemAnnouncementsPage() {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const queryClient = useQueryClient()
  const status = useQuery({ ...statusQueryOptions, refetchInterval: 60_000 })
  const notice = useQuery({
    queryKey: ['notice'],
    queryFn: async () => requireServerSuccess(await getNotice()),
    staleTime: 60_000,
  })
  const items = useMemo(
    () =>
      status.data?.announcements_enabled === true
        ? publishedAnnouncementItems(status.data.announcements)
        : [],
    [status.data]
  )
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['status'] })
    void notice.refetch()
  }
  return (
    <Main>
      <div className='min-h-0 flex-1 overflow-auto p-4 sm:p-6'>
        {(status.isPending || notice.isPending) && <LoadingState />}
        {(status.isError || notice.isError) && <ErrorState onRetry={refresh} />}
        {status.data && (
          <AnnouncementCenter
            readerId={userId}
            items={items}
            notice={notice.data?.data ?? ''}
            refreshing={status.isFetching || notice.isFetching}
            onRefresh={refresh}
          />
        )}
      </div>
    </Main>
  )
}

export function AnnouncementCenter(props: {
  readerId?: number
  items: Announcement[]
  notice: string
  refreshing: boolean
  onRefresh: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [tab, setTab] = useState('announcements')
  const [search, setSearch] = useState('')
  const [unreadOnly, setUnreadOnly] = useState(false)
  const [selected, setSelected] = useState<Announcement | null>(null)
  const readerScope = notificationScope(props.readerId)
  const readKeys = useNotificationStore((state) =>
    readerScope === 'public'
      ? state.readAnnouncementKeys
      : (state.readAnnouncementKeysByUser[readerScope] ??
        EMPTY_NOTIFICATION_KEYS)
  )
  const markRead = useNotificationStore((state) => state.markAnnouncementsRead)
  const markNoticeRead = useNotificationStore((state) => state.markNoticeRead)
  const lastReadNotice = useNotificationStore((state) =>
    readerScope === 'public'
      ? state.lastReadNotice
      : (state.lastReadNoticeByUser[readerScope] ?? '')
  )
  useEffect(() => {
    if (tab === 'notice' && props.notice) {
      markNoticeRead(props.notice, readerScope)
    }
  }, [tab, props.notice, markNoticeRead, readerScope])
  const unreadCount = props.items.filter(
    (item) => !readKeys.includes(getAnnouncementKey(item))
  ).length
  const noticeUnread =
    props.notice.trim() !== lastReadNotice && props.notice.trim().length > 0
  const filtered = props.items.filter((item) => {
    if (tab === 'announcements' && item.category === 'timeline') return false
    if (unreadOnly && readKeys.includes(getAnnouncementKey(item))) return false
    return `${item.title ?? ''} ${getPreviewText(item.content)} ${item.extra ?? ''}`
      .toLocaleLowerCase()
      .includes(search.trim().toLocaleLowerCase())
  })
  const visible =
    tab === 'timeline'
      ? [...filtered].sort(
          (a, b) => Date.parse(b.publishDate) - Date.parse(a.publishDate)
        )
      : filtered
  return (
    <div className='mx-auto max-w-[1200px] space-y-6'>
      <header className='flex flex-wrap items-start justify-between gap-4'>
        <div className='space-y-1.5'>
          <div className='text-primary flex items-center gap-2 text-xs font-medium'>
            <Megaphone className='size-4' aria-hidden />
            {t('Platform updates')}
          </div>
          <h1 className='text-2xl font-semibold tracking-tight'>
            {t('Announcement center')}
          </h1>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Service notices, product updates and release history in one place.'
            )}
          </p>
        </div>
        <div className='flex flex-wrap gap-2'>
          <Button
            variant='outline'
            onClick={props.onRefresh}
            disabled={props.refreshing}
          >
            <RefreshCw className='size-4' aria-hidden />
            {t('Refresh')}
          </Button>
          <Button
            variant='outline'
            disabled={unreadCount === 0 && !noticeUnread}
            onClick={() => {
              markRead(props.items.map(getAnnouncementKey), readerScope)
              if (props.notice) markNoticeRead(props.notice, readerScope)
            }}
          >
            <CheckCheck className='size-4' aria-hidden />
            {t('Mark all as read')}
          </Button>
        </div>
      </header>
      <div className='grid grid-cols-3 gap-3'>
        <div className='bg-card rounded-2xl border p-4'>
          <p className='text-muted-foreground text-xs'>
            {t('Published updates')}
          </p>
          <p className='mt-2 text-2xl font-semibold tabular-nums'>
            {formatNumber(props.items.length, locale)}
          </p>
        </div>
        <div className='bg-card rounded-2xl border p-4'>
          <p className='text-muted-foreground text-xs'>{t('Unread')}</p>
          <p className='mt-2 text-2xl font-semibold tabular-nums'>
            {formatNumber(unreadCount + Number(noticeUnread), locale)}
          </p>
        </div>
        <div className='bg-card rounded-2xl border p-4'>
          <p className='text-muted-foreground text-xs'>{t('Pinned')}</p>
          <p className='mt-2 text-2xl font-semibold tabular-nums'>
            {formatNumber(
              props.items.filter((item) => item.pinned).length,
              locale
            )}
          </p>
        </div>
      </div>
      <Tabs
        value={tab}
        onValueChange={(value) => setTab(String(value))}
        className='gap-5'
      >
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <TabsList>
            <TabsTrigger value='notice'>
              <Bell className='size-4' aria-hidden />
              {t('Site notice')}
            </TabsTrigger>
            <TabsTrigger value='announcements'>
              <Megaphone className='size-4' aria-hidden />
              {t('Announcements')}
            </TabsTrigger>
            <TabsTrigger value='timeline'>
              <Clock3 className='size-4' aria-hidden />
              {t('Timeline')}
            </TabsTrigger>
          </TabsList>
          {tab !== 'notice' && (
            <div className='flex w-full items-center gap-2 sm:w-auto'>
              <Button
                variant={unreadOnly ? 'secondary' : 'outline'}
                aria-pressed={unreadOnly}
                onClick={() => setUnreadOnly(!unreadOnly)}
              >
                {t('Unread only')}
              </Button>
              <div className='relative min-w-0 flex-1 sm:w-64'>
                <Search
                  className='text-muted-foreground absolute top-2.5 left-3 size-4'
                  aria-hidden
                />
                <Input
                  className='pl-9'
                  aria-label={t('Search announcements')}
                  placeholder={t('Search announcements')}
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                />
              </div>
            </div>
          )}
        </div>
        <TabsContent value='notice'>
          {props.notice ? (
            <article className='bg-card space-y-4 rounded-2xl border p-5 sm:p-7'>
              <h2 className='text-lg font-semibold'>{t('Site notice')}</h2>
              <RichContent
                imagePolicy='announcement'
                breaks
                content={props.notice}
              />
            </article>
          ) : (
            <EmptyState
              icon={Bell}
              title={t('No notices at this time')}
              bordered
            />
          )}
        </TabsContent>
        {(['announcements', 'timeline'] as const).map((value) => (
          <TabsContent key={value} value={value} className='space-y-3'>
            {visible.length === 0 && (
              <EmptyState
                icon={Megaphone}
                title={t('No matching announcements')}
                description={t('Try another search or show all updates.')}
                bordered
              />
            )}
            {visible.map((item) => {
              const key = getAnnouncementKey(item)
              const isRead = readKeys.includes(key)
              return (
                <article
                  key={key}
                  className='bg-card min-w-0 rounded-2xl border shadow-xs'
                >
                  <Button
                    variant='ghost'
                    className='h-auto w-full justify-start rounded-2xl p-5 text-left whitespace-normal sm:p-6'
                    onClick={() => {
                      setSelected(item)
                      markRead([key], readerScope)
                    }}
                  >
                    <span className='block min-w-0 flex-1 space-y-3'>
                      <span className='text-muted-foreground flex flex-wrap items-center gap-2 text-xs'>
                        <time dateTime={item.publishDate}>
                          {formatDateTimeObject(new Date(item.publishDate))}
                        </time>
                        {item.pinned && (
                          <Badge variant='secondary'>
                            <Pin className='size-3' aria-hidden />
                            {t('Pinned')}
                          </Badge>
                        )}
                        {!isRead && (
                          <Badge variant='outline'>{t('Unread')}</Badge>
                        )}
                        {item.category === 'timeline' && (
                          <Badge variant='outline'>{t('Release update')}</Badge>
                        )}
                      </span>
                      <span className='block text-base font-semibold [overflow-wrap:anywhere]'>
                        {item.title ||
                          getPreviewText(item.content).slice(0, 120) ||
                          t('Announcement')}
                      </span>
                      <span className='text-muted-foreground line-clamp-2 block text-sm leading-relaxed font-normal [overflow-wrap:anywhere]'>
                        {getPreviewText(item.content)}
                      </span>
                      <span className='text-primary block text-xs font-medium'>
                        {t('Read update')}
                      </span>
                    </span>
                  </Button>
                </article>
              )
            })}
          </TabsContent>
        ))}
      </Tabs>
      <AnnouncementDetailModal
        open={selected !== null}
        onOpenChange={(open) => {
          if (!open) setSelected(null)
        }}
        announcement={selected}
      />
    </div>
  )
}

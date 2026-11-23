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
import { sha256 } from '@noble/hashes/sha2.js'
import { bytesToHex } from '@noble/hashes/utils.js'
import { focusManager, useQuery } from '@tanstack/react-query'
import {
  useCallback,
  useEffect,
  useState,
  useMemo,
  useSyncExternalStore,
} from 'react'

import { useStatus } from '@/hooks/use-status'
import { getNotice } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'
import { readCachedStatus } from '@/lib/status-query'
import { useNotificationStore } from '@/stores/notification-store'

type Announcement = Record<string, unknown>
const EMPTY_ANNOUNCEMENTS: Announcement[] = []
const ANNOUNCEMENT_KEY_PREFIX = 'v2:'
const subscribeFocus = (listener: () => void) =>
  focusManager.subscribe(listener)
const isFocused = () => focusManager.isFocused()

function parseAnnouncements(value: unknown): Announcement[] | undefined {
  if (!Array.isArray(value)) return
  return value.filter(
    (item): item is Announcement =>
      item !== null && typeof item === 'object' && !Array.isArray(item)
  )
}

function announcementContent(item: Announcement) {
  const text = (key: string) => {
    const value = item[key]
    return typeof value === 'string' ? value : ''
  }
  return {
    publishDate: text('publishDate'),
    content: text('content').trim(),
    extra: text('extra').trim(),
    type: text('type'),
    title: text('title').trim(),
    link: text('link').trim(),
  }
}

function getAnnouncementKey(item: Announcement): string {
  const payload = JSON.stringify({
    id: item.id == null ? null : String(item.id),
    ...announcementContent(item),
  })
  return (
    ANNOUNCEMENT_KEY_PREFIX +
    bytesToHex(sha256(new TextEncoder().encode(payload)))
  )
}

function getLegacyAnnouncementKey(item: Announcement): string {
  if (item.id != null) return `id:${item.id}`
  const fingerprint = JSON.stringify(announcementContent(item))
  let hash = 0
  for (let i = 0; i < fingerprint.length; i += 1) {
    hash = ((hash << 5) - hash + fingerprint.charCodeAt(i)) | 0
  }
  return `hash:${hash.toString(36)}`
}

/**
 * Hook to manage notifications (Notice + Announcements)
 * Provides unread counts and read status management
 */
export function useNotifications(options: { autoOpen?: boolean } = {}) {
  // The app headers pass false when the notification button is disabled.
  const enabled = options.autoOpen !== false
  const focused = useSyncExternalStore(subscribeFocus, isFocused, () => true)
  const [popoverOpen, setPopoverOpen] = useState(false)
  const [activeTab, setActiveTab] = useState<'notice' | 'announcements'>(
    'notice'
  )

  // Fetch Notice from API
  const {
    data: noticeResponse,
    isLoading: noticeLoading,
    refetch: refetchNotice,
  } = useQuery({
    queryKey: ['notice'],
    queryFn: async () => requireServerSuccess(await getNotice()),
    enabled,
    staleTime: 1000 * 60 * 5, // 5 minutes
  })

  // Use the same status entry as the dashboard without putting a poll on
  // every page. Opening the announcement view enables a foreground poll.
  const {
    status,
    loading: statusLoading,
    refetchIfAnnouncementStale,
  } = useStatus(
    enabled ? { announcements: popoverOpen ? 'active' : 'return' } : false
  )
  const announcementsEnabled = enabled && status?.announcements_enabled === true
  const rawAnnouncements = useMemo(
    () => parseAnnouncements(status?.announcements),
    [status?.announcements]
  )
  const announcements = useMemo(
    () =>
      (announcementsEnabled ? rawAnnouncements?.slice(0, 20) : undefined) ??
      EMPTY_ANNOUNCEMENTS,
    [announcementsEnabled, rawAnnouncements]
  )
  const announcementKeys = useMemo(
    () => announcements.map(getAnnouncementKey),
    [announcements]
  )

  const {
    lastReadNotice,
    readAnnouncementKeys,
    replaceLegacyAnnouncementKeys,
    markNoticeRead,
    markAnnouncementsRead,
    closedUntilDate,
    autoPopupShown,
    setAutoPopupShown,
    setClosedUntilDate,
  } = useNotificationStore()
  const [cachedAnnouncements] = useState(() =>
    parseAnnouncements(readCachedStatus()?.announcements)
  )
  const effectiveReadKeys = useMemo(() => {
    if (
      !enabled ||
      !readAnnouncementKeys.some(
        (key) => !key.startsWith(ANNOUNCEMENT_KEY_PREFIX)
      )
    ) {
      return readAnnouncementKeys
    }
    // Before versioned keys, an ID alone did not identify a revision. Use the
    // previous cached payload if available; otherwise take the first payload
    // as the baseline, so upgrading doesn't re-alert known read IDs.
    const baseline = cachedAnnouncements ?? rawAnnouncements
    if (!baseline) return readAnnouncementKeys
    const read = new Set(readAnnouncementKeys)
    return [
      ...readAnnouncementKeys.filter((key) =>
        key.startsWith(ANNOUNCEMENT_KEY_PREFIX)
      ),
      ...baseline
        .filter((item) => read.has(getLegacyAnnouncementKey(item)))
        .map(getAnnouncementKey),
    ]
  }, [cachedAnnouncements, enabled, rawAnnouncements, readAnnouncementKeys])
  useEffect(() => {
    if (effectiveReadKeys !== readAnnouncementKeys) {
      replaceLegacyAnnouncementKeys(readAnnouncementKeys, effectiveReadKeys)
    }
  }, [effectiveReadKeys, readAnnouncementKeys, replaceLegacyAnnouncementKeys])

  // Extract notice content
  const noticeContent = noticeResponse?.success
    ? (noticeResponse.data || '').trim()
    : ''

  // Calculate unread counts
  const unreadCounts = useMemo(() => {
    const noticeUnread =
      noticeContent && noticeContent !== lastReadNotice ? 1 : 0

    const read = new Set(effectiveReadKeys)
    const announcementsUnread = announcementKeys.filter(
      (key) => !read.has(key)
    ).length

    return {
      notice: noticeUnread,
      announcements: announcementsUnread,
      total: noticeUnread + announcementsUnread,
    }
  }, [noticeContent, lastReadNotice, announcementKeys, effectiveReadKeys])

  useEffect(() => {
    if (
      focused &&
      popoverOpen &&
      activeTab === 'announcements' &&
      announcementKeys.length > 0
    ) {
      markAnnouncementsRead(announcementKeys)
    }
  }, [activeTab, announcementKeys, focused, markAnnouncementsRead, popoverOpen])

  // Handle popover open
  const handleOpenPopover = useCallback(
    (tab?: 'notice' | 'announcements') => {
      let nextTab = tab || activeTab
      if (nextTab === 'notice' && !noticeContent && announcements.length > 0) {
        nextTab = 'announcements'
      }

      // Mark currently visible content as read when opening the notification center
      if (noticeContent) {
        markNoticeRead(noticeContent)
      }
      setActiveTab(nextTab)
      setAutoPopupShown()
      setPopoverOpen(true)
    },
    [
      activeTab,
      announcements.length,
      noticeContent,
      markNoticeRead,
      setAutoPopupShown,
    ]
  )

  useEffect(() => {
    if (
      !enabled ||
      !focused ||
      !options.autoOpen ||
      autoPopupShown ||
      useNotificationStore.getState().autoPopupShown ||
      noticeLoading ||
      statusLoading ||
      closedUntilDate === new Date().toDateString() ||
      (!noticeContent && unreadCounts.announcements === 0)
    ) {
      return
    }

    // Yield to the user-facing render and allow another header to claim the
    // one-per-page popup before opening a second dialog.
    const timer = setTimeout(() => {
      if (isFocused() && !useNotificationStore.getState().autoPopupShown) {
        handleOpenPopover(
          unreadCounts.announcements > 0 ? 'announcements' : 'notice'
        )
      }
    }, 0)
    return () => clearTimeout(timer)
  }, [
    enabled,
    focused,
    options.autoOpen,
    autoPopupShown,
    noticeLoading,
    statusLoading,
    closedUntilDate,
    noticeContent,
    unreadCounts.announcements,
    handleOpenPopover,
  ])

  const handlePopoverOpenChange = (open: boolean) => {
    if (open) {
      refetchIfAnnouncementStale?.()
      handleOpenPopover(activeTab)
      return
    }

    setPopoverOpen(false)
  }

  return {
    // Data
    notice: noticeContent,
    announcements,
    loading: noticeLoading || statusLoading,

    // Unread counts
    unreadCount: unreadCounts.total,
    unreadNoticeCount: unreadCounts.notice,
    unreadAnnouncementsCount: unreadCounts.announcements,

    // Popover state
    popoverOpen,
    setPopoverOpen: handlePopoverOpenChange,
    activeTab,
    setActiveTab,

    // Actions
    openPopover: handleOpenPopover,
    closePopover: () => setPopoverOpen(false),
    closeToday: () => {
      setClosedUntilDate(new Date().toDateString())
      setPopoverOpen(false)
    },
    refetchNotice,
  }
}

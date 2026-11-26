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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, test, vi } from 'vitest'

const initialAnnouncements = [
  { id: 1, title: 'First', content: 'hello' },
  { id: 2, title: 'Second', content: 'world' },
]

const statusState = {
  status: {
    announcements_enabled: true,
    announcements: initialAnnouncements,
  },
  loading: false,
}

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => statusState,
}))

vi.mock('@/lib/api', () => ({
  getNotice: async () => ({ success: true, data: '' }),
}))

const { useNotifications } = await import('../use-notifications')
const { useNotificationStore } = await import('@/stores/notification-store')

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

beforeEach(() => {
  localStorage.clear()
  statusState.status.announcements = initialAnnouncements
  useNotificationStore.setState({
    lastReadNotice: '',
    readAnnouncementKeys: [],
    closedUntilDate: null,
    autoPopupShown: false,
  })
})

describe('use notifications announcements', () => {
  test('counts unread announcements from stable status data', async () => {
    const { result } = renderHook(() => useNotifications(), { wrapper })

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.announcements).toHaveLength(2)
    expect(result.current.unreadAnnouncementsCount).toBe(2)
    expect(result.current.unreadCount).toBe(2)
  })

  test('marks announcements as read without refetching status', async () => {
    const { result } = renderHook(() => useNotifications(), { wrapper })

    await waitFor(() => {
      expect(result.current.announcements).toHaveLength(2)
    })

    const before = result.current.announcements
    act(() => result.current.openPopover('announcements'))

    await waitFor(() => {
      expect(result.current.unreadAnnouncementsCount).toBe(0)
    })
    expect(result.current.announcements).toBe(before)
  })

  test('a content edit and a new announcement reusing a deleted ID are unread again', async () => {
    const { rerender, result } = renderHook(() => useNotifications(), {
      wrapper,
    })
    act(() => result.current.openPopover('announcements'))
    await waitFor(() => expect(result.current.unreadAnnouncementsCount).toBe(0))
    act(() => result.current.closePopover())

    statusState.status.announcements = [
      { ...initialAnnouncements[0], content: 'edited message' },
      initialAnnouncements[1],
    ]
    rerender()
    expect(result.current.unreadAnnouncementsCount).toBe(1)
    act(() => result.current.openPopover('announcements'))
    await waitFor(() => expect(result.current.unreadAnnouncementsCount).toBe(0))
    act(() => result.current.closePopover())

    statusState.status.announcements = [initialAnnouncements[1]]
    rerender()
    expect(result.current.unreadAnnouncementsCount).toBe(0)
    statusState.status.announcements = [
      { id: 1, title: 'Replacement', content: 'new message with an old ID' },
      initialAnnouncements[1],
    ]
    rerender()
    expect(result.current.unreadAnnouncementsCount).toBe(1)
  })

  test('migrates legacy read IDs using the previous snapshot without suppressing a newer edit', async () => {
    localStorage.setItem('status', JSON.stringify(statusState.status))
    useNotificationStore.setState({
      readAnnouncementKeys: ['id:1', 'id:2', 'id:3'],
    })
    statusState.status.announcements = [
      { ...initialAnnouncements[0], content: 'edited while offline' },
      initialAnnouncements[1],
    ]
    const { result } = renderHook(() => useNotifications(), { wrapper })
    expect(result.current.unreadAnnouncementsCount).toBe(1)
    await waitFor(() => {
      expect(useNotificationStore.getState().readAnnouncementKeys).toHaveLength(
        2
      )
      expect(
        useNotificationStore
          .getState()
          .readAnnouncementKeys.every((key) => key.startsWith('v2:'))
      ).toBe(true)
    })
  })

  test('without prior cache, consumes old read IDs once without opening a duplicate alert', async () => {
    useNotificationStore.setState({ readAnnouncementKeys: ['id:1', 'id:2'] })
    const { result, rerender } = renderHook(
      () => useNotifications({ autoOpen: true }),
      { wrapper }
    )
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.unreadAnnouncementsCount).toBe(0)
    expect(result.current.popoverOpen).toBe(false)
    statusState.status.announcements = [
      initialAnnouncements[0],
      { ...initialAnnouncements[1], content: 'changed after migration' },
    ]
    rerender()
    await waitFor(() => expect(result.current.popoverOpen).toBe(true))
    expect(result.current.activeTab).toBe('announcements')
  })
})

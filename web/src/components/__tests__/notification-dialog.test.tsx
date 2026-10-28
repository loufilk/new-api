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
import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  afterAll,
  afterEach,
  beforeAll,
  beforeEach,
  describe,
  expect,
  test,
  vi,
} from 'vitest'

import { NotificationPopover } from '@/components/notification-popover'
import { useNotifications } from '@/hooks/use-notifications'
import { useNotificationStore } from '@/stores/notification-store'

const fixture = vi.hoisted(() => ({
  loading: false,
  notice: '',
  enabled: true,
  announcements: [
    {
      id: 1,
      content: 'Platform announcement',
      publishDate: '2026-10-10T08:00:00Z',
    },
  ],
}))

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({
    loading: fixture.loading,
    status: {
      announcements_enabled: fixture.enabled,
      announcements: fixture.announcements,
    },
  }),
}))
vi.mock('@/lib/api', () => ({
  getNotice: async () => ({ success: true, data: fixture.notice }),
}))

function NotificationFixture() {
  const notifications = useNotifications({ autoOpen: true })
  return (
    <NotificationPopover
      open={notifications.popoverOpen}
      onOpenChange={notifications.setPopoverOpen}
      unreadCount={notifications.unreadCount}
      activeTab={notifications.activeTab}
      onTabChange={notifications.setActiveTab}
      notice={notifications.notice}
      announcements={notifications.announcements}
      loading={notifications.loading}
      onCloseToday={notifications.closeToday}
    />
  )
}

function renderNotifications() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <NotificationFixture />
    </QueryClientProvider>
  )
  return { ...view, client }
}

const animationsDescriptor = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'getAnimations'
)
beforeAll(() => {
  Object.defineProperty(HTMLElement.prototype, 'getAnimations', {
    configurable: true,
    value: () => [],
  })
})
afterAll(() => {
  if (animationsDescriptor) {
    Object.defineProperty(
      HTMLElement.prototype,
      'getAnimations',
      animationsDescriptor
    )
  } else {
    Reflect.deleteProperty(HTMLElement.prototype, 'getAnimations')
  }
})

beforeEach(() => {
  localStorage.clear()
  fixture.loading = false
  fixture.notice = ''
  fixture.enabled = true
  fixture.announcements = [
    {
      id: 1,
      content: 'Platform announcement',
      publishDate: '2026-10-10T08:00:00Z',
    },
  ]
  useNotificationStore.setState({
    lastReadNotice: '',
    readAnnouncementKeys: [],
    closedUntilDate: null,
    autoPopupShown: false,
  })
})
afterEach(cleanup)

describe('automatic announcement dialog', () => {
  test('available announcements automatically open an accessible large dialog on the announcement tab', async () => {
    renderNotifications()
    const dialog = await screen.findByRole('dialog', {
      name: 'System Announcements',
    })
    expect(
      await within(dialog).findByText('Platform announcement')
    ).toBeVisible()
    expect(
      within(dialog).getByRole('tab', { name: 'System Announcements' })
    ).toHaveAttribute('aria-selected', 'true')
    expect(dialog).toHaveClass('sm:max-w-4xl')
    expect(
      within(dialog).getByRole('button', { name: 'Close Today' })
    ).toBeVisible()
    expect(useNotificationStore.getState().readAnnouncementKeys).toContain(
      'id:1'
    )
  })

  test('regular close stays closed through header remounts and the bell can reopen it', async () => {
    const user = userEvent.setup()
    const view = renderNotifications()
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getAllByRole('button', { name: 'Close' })[0]
    )
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    view.unmount()
    const next = renderNotifications()
    await waitFor(() => expect(next.client.isFetching()).toBe(0))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Notifications' }))
    expect(await screen.findByRole('dialog')).toBeVisible()
    expect(await screen.findByText('Platform announcement')).toBeVisible()
  })

  test('Close Today survives a new page session but still allows manual reopening', async () => {
    const user = userEvent.setup()
    const view = renderNotifications()
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: 'Close Today' })
    )
    expect(useNotificationStore.getState().closedUntilDate).toBe(
      new Date().toDateString()
    )
    expect(
      JSON.parse(localStorage.getItem('notification-storage') ?? '{}').state
        .closedUntilDate
    ).toBe(new Date().toDateString())
    view.unmount()
    act(() => useNotificationStore.setState({ autoPopupShown: false }))
    const next = renderNotifications()
    await waitFor(() => expect(next.client.isFetching()).toBe(0))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Notifications' }))
    expect(await screen.findByRole('dialog')).toBeVisible()
    expect(await screen.findByText('Platform announcement')).toBeVisible()
  })

  test('a prior day dismissal does not block the next visit', async () => {
    useNotificationStore.setState({
      closedUntilDate: new Date(Date.now() - 86400000).toDateString(),
    })
    renderNotifications()
    expect(await screen.findByRole('dialog')).toBeVisible()
  })

  test.each(['empty', 'disabled'] as const)(
    '%s announcements do not open an empty dialog',
    async (state) => {
      if (state === 'empty') fixture.announcements = []
      if (state === 'disabled') fixture.enabled = false
      const view = renderNotifications()
      await waitFor(() => expect(view.client.isFetching()).toBe(0))
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    }
  )

  test('notice-only content opens on the notice tab and Escape closes the dialog', async () => {
    fixture.announcements = []
    fixture.notice = 'Maintenance notice'
    const user = userEvent.setup()
    renderNotifications()
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Maintenance notice')).toBeVisible()
    expect(within(dialog).getByRole('tab', { name: 'Notice' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
    await user.keyboard('{Escape}')
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
  })
})

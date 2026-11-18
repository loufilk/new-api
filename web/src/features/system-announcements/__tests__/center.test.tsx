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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterAll, beforeAll, beforeEach, expect, test, vi } from 'vitest'

import { useNotificationStore } from '@/stores/notification-store'

import { AnnouncementCenter } from '../index'

const animationsDescriptor = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'getAnimations'
)
beforeAll(() =>
  Object.defineProperty(HTMLElement.prototype, 'getAnimations', {
    configurable: true,
    value: () => [],
  })
)
afterAll(() => {
  if (animationsDescriptor) {
    Object.defineProperty(
      HTMLElement.prototype,
      'getAnimations',
      animationsDescriptor
    )
  } else Reflect.deleteProperty(HTMLElement.prototype, 'getAnimations')
})
beforeEach(() => {
  useNotificationStore.setState({
    readAnnouncementKeys: [],
    lastReadNotice: '',
    readAnnouncementKeysByUser: {},
    lastReadNoticeByUser: {},
  })
})
const items = [
  {
    id: 1,
    title: 'New group launch',
    content: 'New model route is available.',
    publishDate: '2026-11-17T12:00:00Z',
    pinned: true,
  },
  {
    id: 2,
    title: 'Routing release',
    content: 'Retry ordering update.',
    publishDate: '2026-11-18T00:00:00Z',
    category: 'timeline' as const,
  },
]

test('opening an update marks only that entry read and unread filtering responds immediately', async () => {
  render(
    <AnnouncementCenter
      items={items}
      notice=''
      refreshing={false}
      onRefresh={vi.fn()}
    />
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: /New group launch/ }))
  expect(
    await screen.findByRole('dialog', { name: 'Announcement Details' })
  ).toBeVisible()
  expect(useNotificationStore.getState().readAnnouncementKeys).toEqual(['id:1'])
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  await user.click(screen.getByRole('button', { name: 'Unread only' }))
  expect(screen.getByText('No matching announcements')).toBeVisible()
  await user.click(screen.getByRole('tab', { name: 'Timeline' }))
  expect(screen.getByRole('button', { name: /Routing release/ })).toBeVisible()
})

test('timeline contains release updates, search narrows entries and notice keeps independent read state', async () => {
  render(
    <AnnouncementCenter
      items={items}
      notice='Service notice text'
      refreshing={false}
      onRefresh={vi.fn()}
    />
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: 'Timeline' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Search announcements' }),
    'Retry'
  )
  expect(screen.getByRole('button', { name: /Routing release/ })).toBeVisible()
  expect(
    screen.queryByRole('button', { name: /New group launch/ })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('tab', { name: 'Site notice' }))
  expect(await screen.findByText('Service notice text')).toBeVisible()
  expect(useNotificationStore.getState().lastReadNotice).toBe(
    'Service notice text'
  )
  expect(useNotificationStore.getState().readAnnouncementKeys).toEqual([])
  await user.click(screen.getByRole('button', { name: 'Mark all as read' }))
  expect(useNotificationStore.getState().readAnnouncementKeys).toEqual([
    'id:1',
    'id:2',
  ])
})

test('switching accounts keeps read state isolated and preserves each account history', async () => {
  const { rerender } = render(
    <AnnouncementCenter
      readerId={11}
      items={items}
      notice=''
      refreshing={false}
      onRefresh={vi.fn()}
    />
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Mark all as read' }))
  expect(
    screen.getByRole('button', { name: 'Mark all as read' })
  ).toBeDisabled()
  rerender(
    <AnnouncementCenter
      readerId={12}
      items={items}
      notice=''
      refreshing={false}
      onRefresh={vi.fn()}
    />
  )
  expect(screen.getByRole('button', { name: 'Mark all as read' })).toBeEnabled()
  expect(
    useNotificationStore.getState().readAnnouncementKeysByUser['user:12']
  ).toBeUndefined()
  rerender(
    <AnnouncementCenter
      readerId={11}
      items={items}
      notice=''
      refreshing={false}
      onRefresh={vi.fn()}
    />
  )
  expect(
    screen.getByRole('button', { name: 'Mark all as read' })
  ).toBeDisabled()
  expect(useNotificationStore.getState().readAnnouncementKeys).toEqual([])
})

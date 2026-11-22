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
import { render, screen } from '@testing-library/react'
import { afterAll, beforeAll, expect, test, vi } from 'vitest'

import { AnnouncementDetailModal } from '@/features/dashboard/components/overview/announcement-detail-dialog'

import { NotificationPopover } from '../notification-popover'

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
  } else {
    Reflect.deleteProperty(HTMLElement.prototype, 'getAnimations')
  }
})

const source =
  'Before\n\n![Schedule](https://images.example.test/a.png)\n\nAfter'

test.each(['notice', 'announcements'] as const)(
  'notification %s tab applies the same safe image presentation',
  async (tab) => {
    render(
      <NotificationPopover
        open
        onOpenChange={vi.fn()}
        unreadCount={1}
        activeTab={tab}
        onTabChange={vi.fn()}
        notice={source}
        announcements={[
          {
            id: 1,
            content: source,
            extra: '![Note](https://images.example.test/note.png)',
          },
        ]}
        loading={false}
        onCloseToday={vi.fn()}
      />
    )
    const image = await screen.findByRole('img', { name: 'Schedule' })
    expect(image).toHaveAttribute('referrerpolicy', 'no-referrer')
    expect(image).toHaveClass('max-w-full', 'h-auto')
    if (tab === 'announcements') {
      expect(screen.getByRole('img', { name: 'Note' })).toHaveAttribute(
        'referrerpolicy',
        'no-referrer'
      )
    }
  }
)

test('dashboard detail renders both announcement and extra image content safely', async () => {
  render(
    <AnnouncementDetailModal
      open
      onOpenChange={vi.fn()}
      announcement={{
        content: source,
        extra: '![Note](https://images.example.test/note.png)',
      }}
    />
  )
  expect(await screen.findByRole('img', { name: 'Schedule' })).toHaveAttribute(
    'referrerpolicy',
    'no-referrer'
  )
  expect(screen.getByRole('img', { name: 'Note' })).toHaveAttribute(
    'loading',
    'lazy'
  )
})

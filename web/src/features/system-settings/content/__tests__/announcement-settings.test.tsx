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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Toaster } from 'sonner'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { AnnouncementsSection } from '../announcements-section'

function renderSettings(data = '[]') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const request = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  render(
    <QueryClientProvider client={client}>
      <AnnouncementsSection enabled data={data} />
      <Toaster />
    </QueryClientProvider>
  )
  return { client, request, user: userEvent.setup() }
}

test('administrator adds a long multi-image announcement, previews it and explicitly saves the original Markdown', async () => {
  const { request, user } = renderSettings()
  await user.click(screen.getByRole('button', { name: 'Add Announcement' }))
  const dialog = screen.getByRole('dialog', { name: 'Add Announcement' })
  const source = `Maintenance schedule\n\n![First](https://images.example.test/a.png?key=${'x'.repeat(600)})\n\n![Second](https://images.example.test/b.png)\n\nText remains.`
  fireEvent.change(within(dialog).getByRole('textbox', { name: 'Content' }), {
    target: { value: source },
  })
  await user.click(within(dialog).getByRole('button', { name: 'Preview' }))
  expect(
    await within(dialog).findByRole('img', { name: 'First' })
  ).toHaveAttribute('referrerpolicy', 'no-referrer')
  expect(within(dialog).getByRole('img', { name: 'Second' })).toBeVisible()
  expect(request).not.toHaveBeenCalled()
  await user.click(within(dialog).getByRole('button', { name: 'Add' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  await user.click(screen.getByRole('button', { name: 'Save Settings' }))
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith('/api/option/', {
      key: 'console_setting.announcements',
      value: expect.any(String),
    })
  )
  const payload = request.mock.calls[0][1] as { value: string }
  expect(JSON.parse(payload.value)[0].content).toBe(source)
})

test('content over 10000 UTF-16 units remains editable and cannot be added', async () => {
  const { request, user } = renderSettings()
  await user.click(screen.getByRole('button', { name: 'Add Announcement' }))
  const dialog = screen.getByRole('dialog', { name: 'Add Announcement' })
  fireEvent.change(within(dialog).getByRole('textbox', { name: 'Content' }), {
    target: { value: `${'😀'.repeat(5000)}x` },
  })
  await user.click(within(dialog).getByRole('button', { name: 'Add' }))
  expect(
    await within(dialog).findByText('Content cannot exceed 10000 characters')
  ).toBeVisible()
  expect(request).not.toHaveBeenCalled()
  expect(within(dialog).getByRole('textbox', { name: 'Content' })).toHaveValue(
    `${'😀'.repeat(5000)}x`
  )
})

test('legacy oversized collections can be read but saving more than 256 KiB is rejected without truncation', async () => {
  const rows = Array.from({ length: 10 }, (_, index) => ({
    id: index + 1,
    content: '中'.repeat(9999),
    publishDate: '2026-11-01T00:00:00Z',
    type: 'default',
  }))
  const { request, user } = renderSettings(JSON.stringify(rows))
  await user.click(screen.getByRole('button', { name: 'Add Announcement' }))
  const dialog = screen.getByRole('dialog', { name: 'Add Announcement' })
  fireEvent.change(within(dialog).getByRole('textbox', { name: 'Content' }), {
    target: { value: 'Additional entry' },
  })
  await user.click(within(dialog).getByRole('button', { name: 'Add' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  await user.click(screen.getByRole('button', { name: 'Save Settings' }))
  expect(
    await screen.findByText('Announcements cannot exceed 256 KiB in total')
  ).toBeVisible()
  expect(request).not.toHaveBeenCalled()
  expect(screen.getByText('Additional entry')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Save Settings' })).toBeEnabled()
})

test('cancelled image drafts do not modify saved announcements', async () => {
  const { request, user } = renderSettings()
  await user.click(screen.getByRole('button', { name: 'Add Announcement' }))
  const dialog = screen.getByRole('dialog', { name: 'Add Announcement' })
  fireEvent.change(within(dialog).getByRole('textbox', { name: 'Content' }), {
    target: { value: '![Draft](https://images.example.test/draft.png)' },
  })
  await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(screen.getByRole('button', { name: 'Save Settings' })).toBeDisabled()
  expect(request).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Add Announcement' }))
  expect(screen.getByRole('textbox', { name: 'Content' })).toHaveValue('')
})

test('an unfinished optional image URL does not block saving valid announcement text', async () => {
  const { request, user } = renderSettings()
  await user.click(screen.getByRole('button', { name: 'Add Announcement' }))
  const dialog = screen.getByRole('dialog', { name: 'Add Announcement' })
  fireEvent.change(within(dialog).getByRole('textbox', { name: 'Content' }), {
    target: { value: 'Keep this text' },
  })
  fireEvent.change(within(dialog).getByLabelText('Image URL'), {
    target: { value: 'unfinished-url' },
  })
  await user.click(within(dialog).getByRole('button', { name: 'Add' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  await user.click(screen.getByRole('button', { name: 'Save Settings' }))
  await waitFor(() => expect(request).toHaveBeenCalledTimes(1))
  const payload = request.mock.calls[0][1] as { value: string }
  expect(JSON.parse(payload.value)[0].content).toBe('Keep this text')
})

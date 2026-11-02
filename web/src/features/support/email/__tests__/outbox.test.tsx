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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Toaster } from 'sonner'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import type { SupportEmailOutboxItem } from '../api'
import { SupportEmailOutbox } from '../outbox'

const clients: QueryClient[] = []
function renderOutbox(status = 'dead') {
  const row: SupportEmailOutboxItem = {
    id: 8,
    ticket_id: 42,
    event: 'admin_reply',
    status,
    attempts: 5,
    next_attempt_at: 0,
    last_error_code: 'SMTP_TRANSIENT',
    created_at: 1,
  }
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
  clients.push(client)
  const get = vi.spyOn(api, 'get').mockImplementation(async () => ({
    data: {
      success: true,
      data: { items: [structuredClone(row)], total: 1 },
    },
  }))
  render(
    <QueryClientProvider client={client}>
      <SupportEmailOutbox userId={7} />
      <Toaster />
    </QueryClientProvider>
  )
  return { row, client, get, user: userEvent.setup() }
}
afterEach(() => clients.splice(0).forEach((client) => client.clear()))

test('retrying a dead notification requires an explicit confirmation and prevents repeated dispatch requests', async () => {
  const { user, row } = renderOutbox()
  let finish: (value: unknown) => void = () => undefined
  const post = vi.spyOn(api, 'post').mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  await user.click(
    await screen.findByRole('button', { name: 'Retry notification #8' })
  )
  const confirmation = await screen.findByRole('alertdialog', {
    name: 'Retry email delivery?',
  })
  expect(
    within(confirmation).getByText(
      'This requeues one failed notification and may send an email. The current settings and verified recipient are checked again.'
    )
  ).toBeVisible()
  expect(post).not.toHaveBeenCalled()
  await user.click(within(confirmation).getByRole('button', { name: 'Retry' }))
  expect(
    within(confirmation).getByRole('button', { name: 'Retry' })
  ).toBeDisabled()
  await user.click(within(confirmation).getByRole('button', { name: 'Retry' }))
  expect(post).toHaveBeenCalledExactlyOnceWith(
    '/api/support-email/outbox/8/retry',
    undefined,
    expect.any(Object)
  )
  row.status = 'pending'
  finish({ data: { success: true } })
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(await screen.findByText('Pending')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Retry notification #8' })
  ).not.toBeInTheDocument()
})

test('dismissing cancellation leaves the queue untouched', async () => {
  const { user } = renderOutbox('retry')
  const post = vi.spyOn(api, 'post')
  await user.click(
    await screen.findByRole('button', { name: 'Cancel notification #8' })
  )
  const dialog = await screen.findByRole('alertdialog', {
    name: 'Cancel queued notification?',
  })
  await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(post).not.toHaveBeenCalled()
  expect(screen.getByText('Retrying')).toBeVisible()
})

test('successful cancellation changes only the chosen pending notification', async () => {
  const { user, row } = renderOutbox('pending')
  const post = vi.spyOn(api, 'post').mockImplementation(async () => {
    row.status = 'cancelled'
    return { data: { success: true } }
  })
  await user.click(
    await screen.findByRole('button', { name: 'Cancel notification #8' })
  )
  const dialog = await screen.findByRole('alertdialog')
  await user.click(within(dialog).getByRole('button', { name: 'Confirm' }))
  expect(await screen.findByText('Cancelled')).toBeVisible()
  expect(post).toHaveBeenCalledExactlyOnceWith(
    '/api/support-email/outbox/8/cancel',
    undefined,
    expect.any(Object)
  )
})

test.each([
  'sending',
  'leased',
  'sent',
  'cancelled',
  'coalesced',
  'future-status',
])('state %s exposes no unsafe retry or cancel action', async (status) => {
  renderOutbox(status)
  await screen.findByRole('table')
  expect(
    screen.queryByRole('button', { name: 'Retry notification #8' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Cancel notification #8' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByText(
      'Mail already approved for dispatch may still be delivered and cannot be recalled.'
    )
  ).toBeVisible()
})

test('an ineligible retry keeps its safe error visible without changing the queue', async () => {
  const { user } = renderOutbox()
  vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: false,
      message: 'Support email notification is no longer eligible',
    },
  })
  await user.click(
    await screen.findByRole('button', { name: 'Retry notification #8' })
  )
  const dialog = await screen.findByRole('alertdialog')
  await user.click(within(dialog).getByRole('button', { name: 'Retry' }))
  expect(
    await screen.findByText('Support email notification is no longer eligible')
  ).toBeVisible()
  expect(dialog).toBeVisible()
  expect(screen.getByText('Delivery failed')).toBeVisible()
})

test('a refreshed sending state disables an already open cancellation confirmation', async () => {
  const { user, row, client } = renderOutbox('pending')
  const post = vi.spyOn(api, 'post')
  await user.click(
    await screen.findByRole('button', { name: 'Cancel notification #8' })
  )
  const dialog = await screen.findByRole('alertdialog')
  row.status = 'sending'
  await client.invalidateQueries({ queryKey: ['support-email-outbox'] })
  await waitFor(() =>
    expect(
      within(dialog).getByRole('button', { name: 'Confirm' })
    ).toBeDisabled()
  )
  await user.click(within(dialog).getByRole('button', { name: 'Confirm' }))
  expect(post).not.toHaveBeenCalled()
  await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(screen.getByText('Sending')).toBeVisible()
})

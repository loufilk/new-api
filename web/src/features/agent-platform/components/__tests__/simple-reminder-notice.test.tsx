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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { SimpleReminderNotice } from '../simple-reminder-notice'

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <SimpleReminderNotice />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...useSystemConfigStore.getState().config.currency,
      quotaPerUnit: 500_000,
    },
  })
})

test('today reminder is visible before acknowledgement and only disappears after server success', async () => {
  const reminder = {
    id: 17,
    agent_user_id: 42,
    quota: 500_000,
    day: '2026-11-03',
  }
  vi.spyOn(api, 'get')
    .mockResolvedValueOnce({ data: { success: true, data: [reminder] } })
    .mockResolvedValue({ data: { success: true, data: [] } })
  const acknowledge = vi
    .spyOn(api, 'request')
    .mockRejectedValueOnce(new Error('Synthetic lost acknowledgement'))
    .mockResolvedValue({ data: { success: true, data: null } })
  mount()
  expect(
    await screen.findByText('Agent 42: overdue ¥1.00 (2026-11-03)')
  ).toBeVisible()
  expect(acknowledge).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'Acknowledge today' }))
  await waitFor(() => expect(acknowledge).toHaveBeenCalledOnce())
  expect(screen.getByRole('dialog')).toBeVisible()
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Acknowledge today' })
    ).toBeEnabled()
  )
  fireEvent.click(screen.getByRole('button', { name: 'Acknowledge today' }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(acknowledge.mock.calls[0][0]).toMatchObject({
    url: '/api/agent-platform/simple/reminders/acknowledge',
    data: { ids: [17] },
  })
  expect(acknowledge.mock.calls[1][0]).toEqual(acknowledge.mock.calls[0][0])
})

test('ordinary users and non-root admins do not request financial reminders', async () => {
  const get = vi.spyOn(api, 'get')
  useAuthStore.getState().auth.setUser({ id: 7, username: 'ordinary', role: 1 })
  const view = mount()
  expect(get).not.toHaveBeenCalled()
  view.unmount()
  useAuthStore.getState().auth.setUser({ id: 8, username: 'admin', role: 10 })
  mount()
  expect(get).not.toHaveBeenCalled()
})

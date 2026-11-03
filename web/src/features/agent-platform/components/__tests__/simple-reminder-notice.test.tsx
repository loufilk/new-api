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

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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { Toaster } from 'sonner'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { AccountPoolSection } from '../account-pool-section'

function Settings() {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  return (
    <SettingsPageProvider actionsContainer={actions}>
      <div ref={setActions} />
      <AccountPoolSection />
      <Toaster />
    </SettingsPageProvider>
  )
}

function renderSettings(
  enabled = false,
  role = 100,
  otherCapabilities: Record<string, unknown> = {}
) {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'owner', role })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  client.setQueryData(['status'], {
    ...otherCapabilities,
    account_pool_enabled: enabled,
  })
  vi.spyOn(api, 'get').mockImplementation(async () => ({
    data: { success: true, data: client.getQueryData(['status']) },
  }))
  return {
    client,
    user: userEvent.setup(),
    ...render(
      <QueryClientProvider client={client}>
        <Settings />
      </QueryClientProvider>
    ),
  }
}
afterEach(() => useAuthStore.getState().auth.reset())

test('root explicitly saves the accessible default-off switch without changing unrelated options', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const { client, user } = renderSettings()
  const toggle = await screen.findByRole('switch', {
    name: 'Enable account pool page',
  })
  expect(toggle).not.toBeChecked()
  expect(toggle).toHaveAttribute(
    'aria-describedby',
    'account-pool-enabled-description'
  )
  expect(toggle).toHaveClass('shrink-0')
  expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
  toggle.focus()
  await user.keyboard(' ')
  expect(toggle).toBeChecked()
  expect(put).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledExactlyOnceWith('/api/option/', {
      key: 'AccountPoolEnabled',
      value: true,
    })
  )
  expect(await screen.findByText('Setting updated successfully')).toBeVisible()
  expect(client.getQueryData(['status'])).toMatchObject({
    account_pool_enabled: true,
  })
})

test('saving disabled state removes old pool cache and updates navigation status', async () => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
  const otherCapabilities = {
    google_oauth: true,
    google_oauth_configured: true,
    announcements_enabled: true,
    announcements: [
      { content: '![Notice](https://images.example.test/a.png)' },
    ],
  }
  const { client, user } = renderSettings(true, 100, otherCapabilities)
  client.setQueryData(['account-pool', 'snapshot', {}], { items: ['old'] })
  await user.click(
    await screen.findByRole('switch', { name: 'Enable account pool page' })
  )
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(client.getQueryData(['status'])).toMatchObject({
      ...otherCapabilities,
      account_pool_enabled: false,
    })
  )
  await waitFor(() =>
    expect(
      client.getQueriesData({ queryKey: ['account-pool', 'snapshot'] })
    ).toHaveLength(0)
  )
})

test('failed save reports the error without changing the effective flag or clearing the draft', async () => {
  vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: false, message: 'Saving is unavailable' },
  })
  const { client, user } = renderSettings()
  await user.click(
    await screen.findByRole('switch', { name: 'Enable account pool page' })
  )
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  expect(await screen.findByText('Saving is unavailable')).toBeVisible()
  expect(screen.getByRole('switch')).toBeChecked()
  expect(client.getQueryData(['status'])).toMatchObject({
    account_pool_enabled: false,
  })
  expect(
    screen.queryByText('Setting updated successfully')
  ).not.toBeInTheDocument()
})

test.each([1, 5, 10])(
  'role %s cannot see or change root-only pool settings',
  (role) => {
    const put = vi.spyOn(api, 'put')
    renderSettings(false, role)
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Save Changes' })
    ).not.toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  }
)

test('root sees the fresh database flag instead of a stale enabled option or status cache', async () => {
  const { client } = renderSettings(true)
  vi.mocked(api.get).mockResolvedValue({
    data: { success: true, data: { account_pool_enabled: false } },
  })
  await client.invalidateQueries({ queryKey: ['status'] })
  await waitFor(() =>
    expect(
      screen.getByRole('switch', { name: 'Enable account pool page' })
    ).not.toBeChecked()
  )
})

test('a pending save disables both the switch and save action against repeated clicks', async () => {
  let resolve: (value: unknown) => void = () => undefined
  const put = vi.spyOn(api, 'put').mockImplementation(
    () =>
      new Promise((done) => {
        resolve = done
      })
  )
  const { user } = renderSettings()
  await user.click(
    await screen.findByRole('switch', { name: 'Enable account pool page' })
  )
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  expect(screen.getByRole('switch')).toHaveAttribute('aria-disabled', 'true')
  const save = screen.getByRole('button', { name: 'Saving...' })
  expect(save).toBeDisabled()
  await user.click(save)
  expect(put).toHaveBeenCalledTimes(1)
  resolve({ data: { success: true } })
  expect(await screen.findByText('Setting updated successfully')).toBeVisible()
})

test('an unreachable current flag never exposes a cached editable setting', async () => {
  const { client } = renderSettings(true)
  await screen.findByRole('switch')
  vi.mocked(api.get).mockRejectedValue(new Error('offline'))
  await client.invalidateQueries({ queryKey: ['status'] })
  expect(await screen.findByText('Failed to load settings')).toBeVisible()
  expect(screen.queryByRole('switch')).not.toBeInTheDocument()
})

test('saving disabled state removes already open diagnostic node details', async () => {
  let enabled = true
  vi.spyOn(api, 'put').mockImplementation(async () => {
    enabled = false
    return { data: { success: true } }
  })
  const { user } = renderSettings(true)
  await screen.findByRole('switch')
  vi.mocked(api.get).mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/status'
          ? { account_pool_enabled: enabled }
          : {
              enabled,
              configured: true,
              invalid_config: false,
              nodes: enabled
                ? [{ channel_id: 42, state: 'ready', count: 3 }]
                : [],
              cache_scope: 'per_process',
              cache_ttl_seconds: 60,
              stale_after_seconds: 300,
              automatic_collection: false,
            },
    },
  }))
  await user.click(screen.getByRole('button', { name: 'pool.diagnostics' }))
  expect(await screen.findByText('42')).toBeVisible()
  await user.click(screen.getByRole('switch'))
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  expect(await screen.findByText('Setting updated successfully')).toBeVisible()
  await waitFor(() => expect(screen.queryByText('42')).not.toBeInTheDocument())
})

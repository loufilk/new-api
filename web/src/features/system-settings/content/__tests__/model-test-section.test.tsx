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
import { ModelTestSection } from '../model-test-section'

function Settings() {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  return (
    <SettingsPageProvider actionsContainer={actions}>
      <div ref={setActions} />
      <ModelTestSection />
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
    model_test_enabled: enabled,
    model_test_settings_available: true,
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
    name: 'Enable model performance tests',
  })
  expect(toggle).not.toBeChecked()
  expect(toggle).toHaveAttribute(
    'aria-describedby',
    'model-test-enabled-description'
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
      key: 'ModelTestEnabled',
      value: true,
    })
  )
  expect(await screen.findByText('Setting updated successfully')).toBeVisible()
  expect(client.getQueryData(['status'])).toMatchObject({
    model_test_enabled: true,
  })
})

test('saving disabled state keeps owned history and changes only the model-test availability', async () => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
  const otherCapabilities = {
    account_pool_enabled: true,
    google_oauth: true,
    announcements_enabled: true,
  }
  const { client, user } = renderSettings(true, 100, otherCapabilities)
  client.setQueryData(['model-tests', 'history'], {
    enabled: true,
    items: [{ id: 'running-owned' }],
  })
  client.setQueryData(['account-pool', 'snapshot'], { items: ['pool'] })
  await user.click(
    await screen.findByRole('switch', {
      name: 'Enable model performance tests',
    })
  )
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(client.getQueryData(['status'])).toMatchObject({
      ...otherCapabilities,
      model_test_enabled: false,
    })
  )
  expect(client.getQueryData(['model-tests', 'history'])).toEqual({
    enabled: false,
    items: [{ id: 'running-owned' }],
  })
  expect(client.getQueryData(['account-pool', 'snapshot'])).toEqual({
    items: ['pool'],
  })
})

test('failed save reports the error without changing the effective flag or clearing the draft', async () => {
  vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: false, message: 'Saving is unavailable' },
  })
  const { client, user } = renderSettings()
  await user.click(
    await screen.findByRole('switch', {
      name: 'Enable model performance tests',
    })
  )
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  expect(await screen.findByText('Saving is unavailable')).toBeVisible()
  expect(screen.getByRole('switch')).toBeChecked()
  expect(client.getQueryData(['status'])).toMatchObject({
    model_test_enabled: false,
  })
  expect(
    screen.queryByText('Setting updated successfully')
  ).not.toBeInTheDocument()
})

test.each([1, 5, 10])(
  'role %s cannot see or change root-only model test settings',
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
    data: {
      success: true,
      data: { model_test_enabled: false, model_test_settings_available: true },
    },
  })
  await client.invalidateQueries({ queryKey: ['status'] })
  await waitFor(() =>
    expect(
      screen.getByRole('switch', { name: 'Enable model performance tests' })
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
    await screen.findByRole('switch', {
      name: 'Enable model performance tests',
    })
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

test.each([
  { model_test_enabled: false, model_test_settings_available: false },
  { model_test_enabled: false },
  { model_test_settings_available: true },
  { model_test_enabled: 'true', model_test_settings_available: true },
])(
  'an unavailable or invalid authoritative status cannot be edited: %j',
  async (status) => {
    const put = vi.spyOn(api, 'put')
    const { client } = renderSettings(true)
    await screen.findByRole('switch')
    vi.mocked(api.get).mockResolvedValue({
      data: { success: true, data: status },
    })
    await client.invalidateQueries({ queryKey: ['status'] })
    expect(await screen.findByText('Failed to load settings')).toBeVisible()
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Save Changes' })
    ).not.toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  }
)

test('a recovered authoritative read restores the current setting without saving a stale draft', async () => {
  const put = vi.spyOn(api, 'put')
  const { client, user } = renderSettings(false)
  await user.click(await screen.findByRole('switch'))
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: { model_test_enabled: false, model_test_settings_available: false },
    },
  })
  await client.invalidateQueries({ queryKey: ['status'] })
  await screen.findByText('Failed to load settings')
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: { model_test_enabled: false, model_test_settings_available: true },
    },
  })
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByRole('switch')).not.toBeChecked()
  expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
  expect(put).not.toHaveBeenCalled()
})

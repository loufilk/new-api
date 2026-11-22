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

import type { SupportEmailSettings } from '@/features/support/email/api'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { SupportEmailSection } from '../support-email-section'

const clients: QueryClient[] = []
function Settings() {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  return (
    <SettingsPageProvider actionsContainer={actions}>
      <div ref={setActions} />
      <SupportEmailSection />
      <Toaster />
    </SettingsPageProvider>
  )
}
function renderSettings(
  role = 100,
  settings: SupportEmailSettings = {
    enabled: false,
    admin_recipients: ['support@example.com'],
    site_origin: 'https://example.com',
    smtp_configured: true,
  }
) {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'owner', role })
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
  clients.push(client)
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/support-email/settings'
          ? structuredClone(settings)
          : { items: [], total: 0 },
    },
  }))
  return {
    client,
    settings,
    get,
    user: userEvent.setup(),
    ...render(
      <QueryClientProvider client={client}>
        <Settings />
      </QueryClientProvider>
    ),
  }
}
afterEach(() => {
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
})

test('root must explicitly save the accessible default-off switch and only support settings are written', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const { user, settings } = renderSettings()
  const toggle = await screen.findByRole('switch', {
    name: 'Enable support email notifications',
  })
  expect(toggle).not.toBeChecked()
  expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
  toggle.focus()
  await user.keyboard(' ')
  expect(toggle).toBeChecked()
  expect(put).not.toHaveBeenCalled()
  settings.enabled = true
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  expect(await screen.findByText('Setting updated successfully')).toBeVisible()
  expect(put).toHaveBeenCalledExactlyOnceWith(
    '/api/support-email/settings',
    {
      enabled: true,
      admin_recipients: ['support@example.com'],
      site_origin: 'https://example.com',
    },
    expect.any(Object)
  )
  expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
})

test('address normalization sends separate canonical recipients', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const { user } = renderSettings()
  const recipients = await screen.findByRole('textbox', {
    name: 'Administrator notification addresses',
  })
  await user.clear(recipients)
  await user.type(
    recipients,
    'Admin@Example.com, admin@example.com; other@example.com'
  )
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/support-email/settings',
      expect.objectContaining({
        admin_recipients: ['admin@example.com', 'other@example.com'],
      }),
      expect.any(Object)
    )
  )
})

test('a failed save preserves the unsaved draft and never displays success', async () => {
  vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: false, message: 'Invalid support email settings' },
  })
  const { user } = renderSettings()
  await user.click(await screen.findByRole('switch'))
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  expect(
    await screen.findByText('Invalid support email settings')
  ).toBeVisible()
  expect(screen.getByRole('switch')).toBeChecked()
  expect(
    screen.queryByText('Setting updated successfully')
  ).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: 'Reset' }))
  expect(screen.getByRole('switch')).not.toBeChecked()
})

test('pending saves disable controls and repeated clicks cannot duplicate the mutation', async () => {
  let finish: (value: unknown) => void = () => undefined
  const put = vi.spyOn(api, 'put').mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  const { user } = renderSettings()
  await user.click(await screen.findByRole('switch'))
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  expect(screen.getByRole('switch')).toHaveAttribute('aria-disabled', 'true')
  expect(
    screen.getByRole('textbox', {
      name: 'Administrator notification addresses',
    })
  ).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Saving...' }))
  expect(put).toHaveBeenCalledTimes(1)
  finish({ data: { success: true } })
  expect(await screen.findByText('Setting updated successfully')).toBeVisible()
})

test('invalid site links show an accessible field error without writing settings', async () => {
  const put = vi.spyOn(api, 'put')
  const { user } = renderSettings()
  const origin = await screen.findByRole('textbox', {
    name: 'Trusted site origin',
  })
  await user.clear(origin)
  await user.type(origin, 'https://evil.example/path?redirect=another')
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  expect(
    await screen.findByText(
      'Enter an HTTPS origin without a path, query or fragment.'
    )
  ).toBeVisible()
  expect(origin).toHaveAttribute('aria-invalid', 'true')
  expect(put).not.toHaveBeenCalled()
})

test.each([1, 5, 10])(
  'role %s cannot fetch or edit root-only support email settings',
  (role) => {
    const { get } = renderSettings(role)
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
    expect(get).not.toHaveBeenCalled()
  }
)

test.each([
  { enabled: 'true' },
  { enabled: true, admin_recipients: [], site_origin: '' },
  null,
])(
  'incomplete or malformed settings %s fail closed instead of exposing cached controls',
  async (invalid) => {
    const { client, get } = renderSettings()
    await screen.findByRole('switch')
    get.mockResolvedValue({ data: { success: true, data: invalid } })
    await client.invalidateQueries({ queryKey: ['support-email-settings'] })
    expect(await screen.findByText('Failed to load settings')).toBeVisible()
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Save Changes' })
    ).not.toBeInTheDocument()
  }
)

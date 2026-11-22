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
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Toaster } from 'sonner'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { SupportEmailPreferences as Preferences } from '../api'
import { SupportEmailPreferences } from '../preferences'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined)
})

const clients: QueryClient[] = []
function renderPreferences(
  data: Preferences = {
    enabled: true,
    email: 'owner@example.com',
    email_verified: true,
    system_enabled: true,
  }
) {
  useAuthStore.getState().auth.setUser({ id: 21, username: 'owner', role: 1 })
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
  clients.push(client)
  const root = createRootRoute()
  const support = createRoute({
    getParentRoute: () => root,
    path: '/support',
    component: SupportEmailPreferences,
  })
  const security = createRoute({
    getParentRoute: () => root,
    path: '/security',
    component: () => <div>Existing account security</div>,
  })
  const router = createRouter({
    routeTree: root.addChildren([support, security]),
    history: createMemoryHistory({ initialEntries: ['/support'] }),
  })
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data } })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
      <Toaster />
    </QueryClientProvider>
  )
  return { client, data, get, router, user: userEvent.setup() }
}
afterEach(() => {
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
})

test('verified owner can opt out without changing email or security mail settings', async () => {
  const { user, data } = renderPreferences()
  const put = vi.spyOn(api, 'put').mockImplementation(async () => {
    data.enabled = false
    return { data: { success: true } }
  })
  expect(await screen.findByText('Verified notification email')).toBeVisible()
  expect(screen.getByText('owner@example.com')).toBeVisible()
  await user.click(
    screen.getByRole('switch', { name: 'Email me when support replies' })
  )
  await waitFor(() => expect(screen.getByRole('switch')).not.toBeChecked())
  expect(put).toHaveBeenCalledExactlyOnceWith(
    '/api/support-email/preferences',
    { enabled: false },
    expect.any(Object)
  )
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
})

test.each([
  { email: '', email_verified: false },
  { email: 'legacy@example.com', email_verified: false },
])(
  'missing or unverified email %s links to the existing account-security verification flow',
  async (email) => {
    const { user, router } = renderPreferences({
      enabled: true,
      system_enabled: false,
      ...email,
    })
    const link = await screen.findByRole('link', { name: 'Account security' })
    expect(link).toHaveAttribute('href', '/security')
    expect(
      screen.getByText(
        'Support email notifications are currently disabled by the site administrator.'
      )
    ).toBeVisible()
    expect(
      screen.queryByText('Verified notification email')
    ).not.toBeInTheDocument()
    await user.click(link)
    expect(await screen.findByText('Existing account security')).toBeVisible()
    expect(router.state.location.pathname).toBe('/security')
  }
)

test('failed preference updates preserve the effective value and never claim success', async () => {
  const { user } = renderPreferences()
  vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: false, message: 'Preference was not saved' },
  })
  await user.click(await screen.findByRole('switch'))
  expect(await screen.findByText('Preference was not saved')).toBeVisible()
  expect(screen.getByRole('switch')).toBeChecked()
  expect(
    screen.queryByText('Setting updated successfully')
  ).not.toBeInTheDocument()
})

test('a pending preference update disables repeated toggles and does not optimistically claim a change', async () => {
  const { user, data } = renderPreferences()
  let finish: (value: unknown) => void = () => undefined
  const put = vi.spyOn(api, 'put').mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  const toggle = await screen.findByRole('switch')
  await user.click(toggle)
  expect(toggle).toHaveAttribute('aria-disabled', 'true')
  expect(toggle).toBeChecked()
  await user.click(toggle)
  expect(put).toHaveBeenCalledTimes(1)
  data.enabled = false
  finish({ data: { success: true } })
  await waitFor(() => expect(toggle).not.toBeChecked())
})

test('malformed verification state fails closed and hides stale email and editable preferences', async () => {
  const { client, get } = renderPreferences()
  await screen.findByText('owner@example.com')
  get.mockResolvedValue({
    data: {
      success: true,
      data: {
        enabled: true,
        email: 'other@example.com',
        email_verified: 'true',
        system_enabled: true,
      },
    },
  })
  await client.invalidateQueries({ queryKey: ['support-email-preferences'] })
  expect(
    await screen.findByText('Failed to load notification preferences')
  ).toBeVisible()
  expect(screen.queryByRole('switch')).not.toBeInTheDocument()
  expect(screen.queryByText('owner@example.com')).not.toBeInTheDocument()
  expect(screen.queryByText('other@example.com')).not.toBeInTheDocument()
})

test('switching accounts never displays the previous account email from cache', async () => {
  const { get } = renderPreferences()
  await screen.findByText('owner@example.com')
  get.mockResolvedValue({
    data: {
      success: true,
      data: {
        enabled: false,
        email: 'second@example.com',
        email_verified: true,
        system_enabled: true,
      },
    },
  })
  act(() =>
    useAuthStore
      .getState()
      .auth.setUser({ id: 22, username: 'second', role: 1 })
  )
  expect(screen.queryByText('owner@example.com')).not.toBeInTheDocument()
  expect(await screen.findByText('second@example.com')).toBeVisible()
  expect(screen.getByRole('switch')).not.toBeChecked()
})

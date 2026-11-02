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
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { Route as SupportRoute } from '@/routes/_authenticated/support/index'
import { useAuthStore } from '@/stores/auth-store'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined)
})

const clients: QueryClient[] = []
function renderSupport(entry = '/support?ticket=41', role = 1) {
  useAuthStore.getState().auth.setUser({ id: 21, username: 'owner', role })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  clients.push(client)
  const root = createRootRoute({ component: Outlet })
  const auth = createRoute({
    getParentRoute: () => root,
    id: '_authenticated',
    component: Outlet,
  })
  const support = createRoute({
    getParentRoute: () => auth,
    path: '/support/',
    validateSearch: SupportRoute.options.validateSearch,
    component: SupportRoute.options.component,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([support])]),
    history: createMemoryHistory({ initialEntries: [entry] }),
  })
  const ticket = (id: number) => ({
    id,
    user_id: 21,
    subject: `Ticket subject ${id}`,
    status: 'awaiting_user',
    user_unread: false,
    admin_unread: false,
    last_message_at: 1,
    created_at: 1,
    updated_at: 1,
  })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/support-email/preferences') {
      return {
        data: {
          success: true,
          data: {
            enabled: true,
            email: 'owner@example.com',
            email_verified: true,
            system_enabled: true,
          },
        },
      }
    }
    if (
      url === '/api/support/tickets' ||
      url === '/api/admin/support/tickets'
    ) {
      return {
        data: {
          success: true,
          data: {
            items: [ticket(41), ticket(42)],
            total: 2,
            status_counts: { processing: 0, awaiting_user: 2, resolved: 0 },
          },
        },
      }
    }
    const id = Number(String(url).split('/').at(-1))
    return {
      data: {
        success: true,
        data: {
          ticket: ticket(id),
          messages: [
            {
              id,
              ticket_id: id,
              author_role: 'admin',
              content: `Reply for ticket ${id}`,
              created_at: 1,
            },
          ],
        },
      },
    }
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return { router, get, user: userEvent.setup() }
}
afterEach(() => {
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
})

test('an email deep link opens the requested ticket and Back/Forward follows ticket selection', async () => {
  const { router, user } = renderSupport()
  expect(await screen.findByText('Reply for ticket 41')).toBeVisible()
  await user.click(screen.getByRole('button', { name: /Ticket subject 42/ }))
  expect(await screen.findByText('Reply for ticket 42')).toBeVisible()
  expect(router.state.location.search).toMatchObject({ ticket: 42 })
  act(() => router.history.back())
  expect(await screen.findByText('Reply for ticket 41')).toBeVisible()
  act(() => router.history.forward())
  expect(await screen.findByText('Reply for ticket 42')).toBeVisible()
})

test('invalid email ticket IDs never trigger a detail request or a redirect', async () => {
  const { router, get } = renderSupport(
    '/support?ticket=https://evil.example&redirect=https://evil.example'
  )
  expect(await screen.findByText('Select a ticket')).toBeVisible()
  expect(get.mock.calls.some(([url]) => /\/tickets\//.test(String(url)))).toBe(
    false
  )
  expect(router.state.location.pathname).toBe('/support')
})

test('administrator deep links use only administrator ticket detail routes', async () => {
  const { get } = renderSupport('/support?ticket=41', 100)
  expect(await screen.findByText('Reply for ticket 41')).toBeVisible()
  expect(get).toHaveBeenCalledWith('/api/admin/support/tickets/41')
  expect(get).not.toHaveBeenCalledWith('/api/support/tickets/41')
})

test('a different user cannot see the previous owner conversation from the query cache', async () => {
  const { get } = renderSupport()
  await screen.findByText('Reply for ticket 41')
  get.mockImplementation(async (url) => {
    if (url === '/api/support/tickets/41') {
      return { data: { success: false, message: 'Not found' } }
    }
    if (url === '/api/support-email/preferences') {
      return {
        data: {
          success: true,
          data: {
            enabled: true,
            email: 'next@example.com',
            email_verified: true,
            system_enabled: true,
          },
        },
      }
    }
    return {
      data: {
        success: true,
        data: {
          items: [],
          total: 0,
          status_counts: { processing: 0, awaiting_user: 0, resolved: 0 },
        },
      },
    }
  })
  act(() =>
    useAuthStore.getState().auth.setUser({ id: 22, username: 'next', role: 1 })
  )
  expect(screen.queryByText('Reply for ticket 41')).not.toBeInTheDocument()
  expect(
    await screen.findByText('Failed to load ticket conversation')
  ).toBeVisible()
  await waitFor(() =>
    expect(screen.queryByText('Ticket subject 41')).not.toBeInTheDocument()
  )
})

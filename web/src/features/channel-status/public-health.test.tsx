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
  createRootRoute,
  createRoute,
  createRouter,
  createMemoryHistory,
  RouterProvider,
} from '@tanstack/react-router'
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor,
} from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, expect, test, vi } from 'vitest'

import { Route } from '@/routes/health'

vi.mock('@/components/layout', () => ({
  PublicLayout: ({ children }: { children: ReactNode }) => <>{children}</>,
}))

let client: QueryClient
afterEach(() => {
  cleanup()
  client?.clear()
  vi.unstubAllGlobals()
})

function openHealth() {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const root = createRootRoute()
  const route = createRoute({
    getParentRoute: () => root,
    path: '/health',
    component: Route.options.component,
  })
  const destination = createRoute({
    getParentRoute: () => root,
    path: '/channel-status',
  })
  const router = createRouter({
    routeTree: root.addChildren([route, destination]),
    history: createMemoryHistory({ initialEntries: ['/health'] }),
  })
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

test('guest sees only published monitor heartbeats; failed refresh does not present a stale service as healthy', async () => {
  const request = vi
    .fn()
    .mockResolvedValueOnce(
      Response.json({
        success: true,
        data: [
          {
            categoryName: 'Public API',
            state: 'ready',
            monitors: [
              { name: 'Gateway', status: 1 },
              { name: 'No heartbeat', status: -1 },
            ],
          },
          {
            categoryName: 'Old sample',
            state: 'stale',
            updated_at: 1700000000,
            monitors: [{ name: 'Public CDN', status: 2 }],
          },
          { categoryName: 'Not reporting', state: 'unavailable', monitors: [] },
        ],
      })
    )
    .mockResolvedValueOnce(new Response(null, { status: 503 }))
  vi.stubGlobal('fetch', request)
  openHealth()
  expect(await screen.findByText('Gateway')).toBeVisible()
  expect(screen.getByText('Operational')).toBeVisible()
  expect(screen.getByText('Unknown')).toBeVisible()
  expect(
    screen.getByText('Showing the last reported status.', { exact: false })
  ).toBeVisible()
  expect(screen.getByText('Unavailable')).toBeVisible()
  expect(
    screen.getByRole('link', { name: 'View my group status' })
  ).toHaveAttribute('href', '/channel-status')
  expect(request).toHaveBeenCalledTimes(1)
  expect(request).toHaveBeenCalledWith(
    '/api/uptime/status',
    expect.objectContaining({ credentials: 'omit', cache: 'no-store' })
  )
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Unable to load public monitoring data.'
  )
  expect(screen.queryByText('Operational')).not.toBeInTheDocument()
})

test('disabled or unconfigured monitors show no data, never healthy; failed response envelope shows failure', async () => {
  const request = vi
    .fn()
    .mockResolvedValueOnce(Response.json({ success: true, data: [] }))
    .mockResolvedValueOnce(Response.json({ success: false, data: [] }))
  vi.stubGlobal('fetch', request)
  openHealth()
  expect(await screen.findByText('No public monitoring data')).toBeVisible()
  expect(screen.queryByText('Operational')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  await waitFor(() => expect(screen.getByRole('alert')).toBeVisible())
})

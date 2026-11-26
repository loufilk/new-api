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
  PublicLayout: ({ children }: { children: ReactNode }) => (
    <div>{children}</div>
  ),
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
  const fetch = vi.fn((url: string) =>
    url === '/api/uptime/lines'
      ? Promise.resolve(
          Response.json({
            success: true,
            data: { lines: [], window_start: 0, window_end: 86400 },
          })
        )
      : request(url)
  )
  vi.stubGlobal('fetch', fetch)
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
  expect(fetch).toHaveBeenCalledWith(
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
  const fetch = vi.fn((url: string) =>
    url === '/api/uptime/lines'
      ? Promise.resolve(
          Response.json({
            success: true,
            data: { lines: [], window_start: 0, window_end: 86400 },
          })
        )
      : request(url)
  )
  vi.stubGlobal('fetch', fetch)
  openHealth()
  expect(await screen.findByText('No public monitoring data')).toBeVisible()
  expect(screen.queryByText('Operational')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  await waitFor(() => expect(screen.getByRole('alert')).toBeVisible())
})

test('guest lines filter by vendor, sort by latency, open hourly history and hide revoked lines after refresh', async () => {
  const measurement = {
    success_rate: 95,
    average_latency_ms: 350,
    cache_hit_rate: 0,
  }
  const line = {
    id: 'public-a',
    label: 'Alpha public',
    representative_model: 'alpha-1',
    vendor: 'Alpha',
    ratio: 1,
    sort_order: 1,
    ...measurement,
    buckets: [{ bucket_start: 82800, ...measurement }],
  }
  const payload = (lines: object[]) =>
    Response.json({
      success: true,
      data: { window_start: 0, window_end: 86400, lines },
    })
  const readLines = vi
    .fn()
    .mockImplementationOnce(() =>
      Promise.resolve(
        payload([
          line,
          {
            ...line,
            id: 'public-b',
            label: 'Beta public',
            representative_model: 'beta-1',
            vendor: 'Beta',
            sort_order: 2,
            average_latency_ms: 110,
            cache_hit_rate: null,
          },
        ])
      )
    )
    .mockImplementation(() => Promise.resolve(payload([])))
  const fetch = vi.fn((url: string, init: RequestInit) =>
    url === '/api/uptime/lines'
      ? readLines(url, init)
      : Promise.resolve(Response.json({ success: true, data: [] }))
  )
  vi.stubGlobal('fetch', fetch)
  openHealth()
  expect(
    await screen.findByRole('heading', { name: 'Alpha public' })
  ).toBeVisible()
  expect(readLines).toHaveBeenCalledWith(
    '/api/uptime/lines',
    expect.objectContaining({ credentials: 'omit', cache: 'no-store' })
  )
  expect(screen.getAllByText('0%', { selector: 'dd' })[0]).toBeVisible()
  expect(screen.getByText('–', { selector: 'dd' })).toBeVisible()
  fireEvent.change(screen.getByLabelText('Sort by'), {
    target: { value: 'average_latency_ms' },
  })
  const heading = screen
    .getAllByRole('heading', { level: 3 })
    .map((node) => node.textContent)
  expect(heading).toEqual(['Beta public', 'Alpha public'])
  fireEvent.change(screen.getByLabelText('Vendor'), {
    target: { value: 'Alpha' },
  })
  expect(
    screen.queryByRole('heading', { name: 'Beta public' })
  ).not.toBeInTheDocument()
  fireEvent.click(screen.getByText('Hourly history'))
  expect(screen.getByText('Hourly history').parentElement).toHaveAttribute(
    'open'
  )
  fireEvent.change(screen.getByLabelText('Auto-refresh interval'), {
    target: { value: '0' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(await screen.findByText('No public line metrics')).toBeVisible()
  expect(
    screen.queryByRole('heading', { name: 'Alpha public' })
  ).not.toBeInTheDocument()
})

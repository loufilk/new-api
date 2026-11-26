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
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { chuanapiSidebarNavigationExtension } from '@/extensions/chuanapi/navigation'
import en from '@/i18n/locales/en.json'
import { Route as ChannelStatusRoute } from '@/routes/_authenticated/channel-status'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  api: { get },
}))
vi.mock('@/stores/auth-store', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/stores/auth-store')>()),
  useAuthStore: Object.assign(
    (select: (state: unknown) => unknown) =>
      select({
        auth: { user: { id: 7 }, session: { sid: 'fixture-session' } },
      }),
    { subscribe: () => () => undefined }
  ),
}))
vi.mock('@/lib/auth-session', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/auth-session')>()),
  getAuthSessionEpoch: () => 8,
}))

const i18n = createInstance()
await i18n.init({ lng: 'en', resources: { en }, initAsync: false })
let queryClient: QueryClient

function response(data: unknown) {
  return { data: { success: true, data } }
}
const windowStart = 1_700_002_800
function health(group: string, successRate: number, ttft: number) {
  return {
    group,
    available: true,
    metrics_available: true,
    window_start: windowStart,
    observed_at: windowStart + 23 * 3600 + 30,
    bucket_seconds: 3600,
    success_rate: successRate,
    average_ttft_ms: ttft,
    buckets: Array.from({ length: 24 }, (_, index) => ({
      bucket_start: windowStart + index * 3600,
      success_rate: index === 23 ? successRate : null,
      average_ttft_ms: index === 23 ? ttft : null,
    })),
  }
}

beforeEach(() => {
  queryClient = new QueryClient()
  get.mockImplementation(async (url: string, config) => {
    expect(config.params.view).toBe('channel-status')
    expect(config.authSessionEpoch).toBe(8)
    expect(config.signal).toBeInstanceOf(AbortSignal)
    if (url === '/api/user/self/groups') {
      return response({
        cheap: {
          ratio: 0.2,
          desc: 'Public group on OpenAI',
          catalog: { label: 'Affordable', platform: 'openai' },
        },
        fast: {
          ratio: 0.7,
          desc: 'Public group on Anthropic',
          catalog: { label: 'Fast', platform: 'anthropic' },
        },
        changed: {
          ratio: 0.4,
          desc: 'Changed since catalog',
          catalog: { label: 'Revoked Group' },
        },
        hidden: {
          ratio: 0.1,
          desc: 'Exclusive tenant',
          catalog: { label: 'Exclusive Group', exclusive: true },
        },
        auto: { ratio: 'auto', desc: 'Virtual choice' },
        all: { ratio: 1, desc: 'Virtual group' },
      })
    }
    expect(url).toBe('/api/token/group-health')
    const group = config.params.group
    if (group === 'cheap') return response(health(group, 91, 183))
    if (group === 'fast') return response(health(group, 100, 68))
    if (group === 'changed') throw new Error('Group became unavailable')
    throw new Error('An ineligible group was requested')
  })
})
afterEach(() => {
  cleanup()
  queryClient.clear()
  get.mockReset()
})

function openStatusFromSidebar() {
  const sidebar = chuanapiSidebarNavigationExtension.apply(
    { navGroups: [{ id: 'personal', title: 'Personal', items: [] }] },
    { isAuthenticated: true, user: { id: 7, role: 1, username: 'fixture' } }
  )
  const link = sidebar.navGroups[0]?.items.find(
    (item) => 'url' in item && item.url === '/channel-status'
  )
  if (!link || !('url' in link)) {
    throw new Error('Missing authenticated sidebar link')
  }
  const root = createRootRoute()
  const status = createRoute({
    getParentRoute: () => root,
    path: '/channel-status',
    component: ChannelStatusRoute.options.component,
  })
  const keys = createRoute({
    getParentRoute: () => root,
    path: '/keys',
    component: () => <h1>Existing API Keys screen</h1>,
  })
  const router = createRouter({
    routeTree: root.addChildren([status, keys]),
    history: createMemoryHistory({ initialEntries: [String(link.url)] }),
  })
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </I18nextProvider>
  )
  return router
}

test('sidebar entry renders only authorized aggregate groups and supports filtering and sorting', async () => {
  openStatusFromSidebar()
  expect(
    await screen.findByRole('heading', { name: 'Channel status' })
  ).toBeVisible()
  expect(
    await screen.findByRole('heading', { name: 'Affordable' })
  ).toBeVisible()
  expect(screen.getByRole('heading', { name: 'Fast' })).toBeVisible()
  expect(screen.queryByText('Exclusive Group')).not.toBeInTheDocument()
  expect(screen.queryByText('Revoked Group')).not.toBeInTheDocument()
  expect(
    get.mock.calls
      .slice(1)
      .map(([, config]) => config.params.group)
      .sort()
  ).toEqual(['changed', 'cheap', 'fast'])
  const names = () =>
    screen
      .getAllByRole('heading', { level: 3 })
      .map((heading) => heading.textContent)
  expect(names()).toEqual(['Affordable', 'Fast'])
  fireEvent.change(screen.getByRole('combobox', { name: 'Sort groups' }), {
    target: { value: 'ttft' },
  })
  expect(names()).toEqual(['Fast', 'Affordable'])
  fireEvent.click(screen.getByRole('button', { name: 'OpenAI (1)' }))
  expect(names()).toEqual(['Affordable'])
  expect(screen.getByText('91%')).toBeVisible()
  expect(screen.queryByText('Request count')).not.toBeInTheDocument()
  expect(screen.getByText('Some health results are unavailable.')).toBeVisible()
})

test('offers actual hourly history, marks longer periods unavailable, and passes only one group to API Keys', async () => {
  const router = openStatusFromSidebar()
  fireEvent.click(
    await screen.findByRole('button', { name: 'Details for Affordable' })
  )
  const dialog = await screen.findByRole('dialog', { name: 'Affordable' })
  expect(within(dialog).getByRole('table')).toBeVisible()
  expect(within(dialog).getAllByRole('row')).toHaveLength(25)
  expect(within(dialog).getByText('91%')).toBeVisible()
  fireEvent.click(within(dialog).getByRole('button', { name: 'Last 30 days' }))
  expect(
    within(dialog).getByText(
      'Only the last 24 hours of group history are available.'
    )
  ).toBeVisible()
  expect(within(dialog).queryByRole('table')).not.toBeInTheDocument()
  fireEvent.click(within(dialog).getByRole('button', { name: 'Last 12 weeks' }))
  expect(within(dialog).queryByRole('table')).not.toBeInTheDocument()
  fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  fireEvent.click(
    screen.getByRole('link', { name: 'Create API key for Affordable' })
  )
  expect(
    await screen.findByRole('heading', { name: 'Existing API Keys screen' })
  ).toBeVisible()
  const params = new URLSearchParams(router.state.location.searchStr)
  expect([...params.entries()].sort()).toEqual([
    ['create', 'true'],
    ['group', 'cheap'],
  ])
})

test('can stop auto-refresh without stopping manual refresh', async () => {
  openStatusFromSidebar()
  await screen.findByRole('heading', { name: 'Affordable' })
  const auto = screen.getByRole('button', { name: 'Auto-refresh' })
  expect(auto).toHaveAttribute('aria-pressed', 'true')
  fireEvent.click(auto)
  expect(auto).toHaveAttribute('aria-pressed', 'false')
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  await waitFor(() =>
    expect(
      get.mock.calls.filter(([url]) => url === '/api/user/self/groups')
    ).toHaveLength(2)
  )
})

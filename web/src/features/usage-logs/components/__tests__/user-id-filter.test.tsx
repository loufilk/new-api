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
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { PageFooterProvider } from '@/components/layout/components/page-footer'
import { api } from '@/lib/api'
import { Route as UsageLogsRoute } from '@/routes/_authenticated/usage-logs/$section'
import { useAuthStore } from '@/stores/auth-store'

import { usageLogSchema } from '../../data/schema'
import { UsageLogsProvider, useUsageLogsContext } from '../usage-logs-provider'
import { UsageLogsTable } from '../usage-logs-table'

const clients: QueryClient[] = []
const captureDescriptor = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'setPointerCapture'
)

function RouteError(props: { error: Error }) {
  return <div role='alert'>{props.error.message}</div>
}

function LogsFixture() {
  const { viewScope, setViewScope } = useUsageLogsContext()
  const [footer, setFooter] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <button
        type='button'
        onClick={() => setViewScope(viewScope === 'all' ? 'self' : 'all')}
      >
        Switch scope
      </button>
      <PageFooterProvider container={footer}>
        <UsageLogsTable logCategory='common' />
      </PageFooterProvider>
      <div ref={setFooter} />
    </>
  )
}

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  localStorage.clear()
})

afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
  localStorage.clear()
  vi.restoreAllMocks()
  if (captureDescriptor) {
    Object.defineProperty(
      HTMLElement.prototype,
      'setPointerCapture',
      captureDescriptor
    )
  } else {
    Reflect.deleteProperty(HTMLElement.prototype, 'setPointerCapture')
  }
})

async function renderLogs(
  initialEntry = '/usage-logs/common',
  role = 10,
  invalidSearch = false
) {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'viewer', role })
  const record = usageLogSchema.parse({
    id: 1,
    user_id: 42,
    created_at: 1788840000,
    type: 2,
    username: '123',
    token_name: 'previous-token',
    content: '',
    other: '{}',
  })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    let data: unknown = { quota: 0, rpm: 17, tpm: 0 }
    if (url === '/api/group/' || url === '/api/subscription/admin/plans') {
      data = []
    } else if (url === '/api/user/self/groups') {
      data = {}
    } else if (url === '/api/subscription/self') {
      data = { subscriptions: [], all_subscriptions: [] }
    } else if (
      url.startsWith('/api/log?') ||
      url.startsWith('/api/log/self?')
    ) {
      data = { items: [record], total: 201 }
    }
    return { data: { success: true, data } }
  })
  const root = createRootRoute({ errorComponent: RouteError })
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    component: LogsFixture,
    validateSearch: UsageLogsRoute.options.validateSearch,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <UsageLogsProvider>
        <RouterProvider router={router} />
      </UsageLogsProvider>
    </QueryClientProvider>
  )
  if (invalidSearch) {
    await screen.findByRole('alert')
  } else {
    await screen.findByRole('combobox', { name: 'Type' })
    await waitFor(() => expect(client.isFetching()).toBe(0))
  }
  return { router, get }
}

it('keeps numeric usernames separate and sends the applied ID to both logs and statistics', async () => {
  const { router, get } = await renderLogs('/usage-logs/common?page=3')
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: /^Expand/ }))
  await user.type(screen.getByRole('textbox', { name: 'User ID' }), '42')
  await user.type(screen.getByPlaceholderText('Username'), '123')
  expect(router.state.location.search).not.toHaveProperty('userId')
  get.mockClear()
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      userId: '42',
      username: '123',
      page: 1,
    })
  )
  for (const path of ['/api/log', '/api/log/stat']) {
    await waitFor(() => {
      const request = get.mock.calls
        .map(([url]) => new URL(url, 'http://localhost'))
        .find(
          (url) =>
            url.pathname === path && url.searchParams.get('user_id') === '42'
        )
      expect(request?.searchParams.get('username')).toBe('123')
    })
  }
})

it('retains the exact ID and other filters when paginating the log table', async () => {
  const { router, get } = await renderLogs(
    '/usage-logs/common?userId=%229007199254740993%22&username=alice&model=gpt-6&group=default'
  )
  get.mockClear()
  await userEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      userId: '9007199254740993',
      username: 'alice',
      model: 'gpt-6',
      group: 'default',
      page: 2,
    })
  )
  await waitFor(() => {
    const request = get.mock.calls
      .map(([url]) => new URL(url, 'http://localhost'))
      .find(
        (url) =>
          url.pathname === '/api/log' && url.searchParams.get('p') === '2'
      )
    expect(request?.searchParams.get('user_id')).toBe('9007199254740993')
    expect(request?.searchParams.get('username')).toBe('alice')
    expect(request?.searchParams.get('model_name')).toBe('gpt-6')
    expect(request?.searchParams.get('group')).toBe('default')
  })
})

it.each([' 42', '9223372036854775808'])(
  'shows an accessible error and blocks Search and Enter for invalid ID %s',
  async (invalidId) => {
    const { router, get } = await renderLogs()
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /^Expand/ }))
    const input = screen.getByRole('textbox', { name: 'User ID' })
    await user.type(input, invalidId)
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input).toHaveAccessibleDescription(
      'User ID must be a positive integer no greater than 9223372036854775807'
    )
    get.mockClear()
    await user.keyboard('{Enter}')
    await user.click(screen.getByRole('button', { name: 'Search' }))
    expect(router.state.location.search).not.toHaveProperty('userId')
    expect(get).not.toHaveBeenCalled()
    await user.clear(input)
    expect(input).toHaveAttribute('aria-invalid', 'false')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  }
)

it('restores exact IDs on Back and Forward and clears them with Reset', async () => {
  const { router } = await renderLogs(
    '/usage-logs/common?userId=%229007199254740993%22'
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: /^Expand/ }))
  const input = screen.getByRole('textbox', { name: 'User ID' })
  expect(input).toHaveValue('9007199254740993')
  await user.clear(input)
  await user.type(input, '9223372036854775807{Enter}')
  await waitFor(() =>
    expect(router.state.location.search).toHaveProperty(
      'userId',
      '9223372036854775807'
    )
  )
  router.history.back()
  await waitFor(() => expect(input).toHaveValue('9007199254740993'))
  router.history.forward()
  await waitFor(() => expect(input).toHaveValue('9223372036854775807'))
  await user.click(screen.getByRole('button', { name: 'Reset' }))
  await waitFor(() => expect(input).toHaveValue(''))
  expect(router.state.location.search).not.toHaveProperty('userId')
})

it.each([1, 5])(
  'hides administrator ID filters for personal role %s and strips them from requests',
  async (role) => {
    const { get } = await renderLogs(
      '/usage-logs/common?userId=42&username=other',
      role
    )
    await userEvent.click(screen.getByRole('button', { name: /^Expand/ }))
    expect(
      screen.queryByRole('textbox', { name: 'User ID' })
    ).not.toBeInTheDocument()
    for (const path of ['/api/log/self', '/api/log/self/stat']) {
      const request = get.mock.calls
        .map(([url]) => new URL(url, 'http://localhost'))
        .find((url) => url.pathname === path)
      expect(request).toBeDefined()
      expect(request?.searchParams.has('user_id')).toBe(false)
      expect(request?.searchParams.has('username')).toBe(false)
    }
  }
)

it('stops applying the ID when an administrator switches to only their own logs', async () => {
  const { get } = await renderLogs(
    '/usage-logs/common?userId=42&username=other'
  )
  get.mockClear()
  await userEvent.click(screen.getByRole('button', { name: 'Switch scope' }))
  await waitFor(() => {
    const request = get.mock.calls
      .map(([url]) => new URL(url, 'http://localhost'))
      .find((url) => url.pathname === '/api/log/self')
    expect(request).toBeDefined()
    expect(request?.searchParams.has('user_id')).toBe(false)
    expect(request?.searchParams.has('username')).toBe(false)
  })
  await userEvent.click(screen.getByRole('button', { name: /^Expand/ }))
  expect(
    screen.queryByRole('textbox', { name: 'User ID' })
  ).not.toBeInTheDocument()
})

it('keeps mobile filters open after an invalid ID and closes only after a valid search', async () => {
  const originalMatchMedia = window.matchMedia
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    ...originalMatchMedia(query),
    matches: query === '(max-width: 640px)',
  }))
  Object.defineProperty(HTMLElement.prototype, 'setPointerCapture', {
    configurable: true,
    value: vi.fn(),
  })
  // Type lives inside the mobile drawer; open it before waiting for fields.
  const pending = renderLogs()
  await userEvent.click(await screen.findByRole('button', { name: /^Filter/ }))
  const { router } = await pending
  const dialog = screen.getByRole('dialog')
  const input = within(dialog).getByRole('textbox', { name: 'User ID' })
  await userEvent.type(input, '0')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Search' }))
  expect(dialog).toBeVisible()
  expect(within(dialog).getByRole('alert')).toBeVisible()
  expect(router.state.location.search).not.toHaveProperty('userId')
  await userEvent.clear(input)
  await userEvent.type(input, '42')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(router.state.location.search).toHaveProperty('userId', '42')
  )
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
})

it.each(['user ID', 'view scope'])(
  'does not show previous logs or statistics while a different %s is loading',
  async (change) => {
    const { get } = await renderLogs('/usage-logs/common?userId=42')
    expect(screen.getByText('previous-token')).toBeVisible()
    expect(screen.getByText('17')).toBeVisible()
    let completeRequest: () => void = () => {}
    const pendingRequest = new Promise<void>((resolve) => {
      completeRequest = resolve
    })
    const previousGet = get.getMockImplementation()
    if (!previousGet) {
      throw new Error('Expected the API fixture to be installed')
    }
    const nextRecord = usageLogSchema.parse({
      id: 2,
      user_id: 43,
      created_at: 1788840000,
      type: 2,
      token_name: 'next-token',
      content: '',
      other: '{}',
    })
    get.mockImplementation(async (url, config) => {
      const request = new URL(url, 'http://localhost')
      const changedScope = request.pathname.startsWith('/api/log/self')
      const changedId = request.searchParams.get('user_id') === '43'
      if (changedScope || changedId) {
        await pendingRequest
        return {
          data: {
            success: true,
            data: request.pathname.endsWith('/stat')
              ? { quota: 0, rpm: 23, tpm: 0 }
              : { items: [nextRecord], total: 1 },
          },
        }
      }
      return previousGet(url, config)
    })
    if (change === 'user ID') {
      await userEvent.click(screen.getByRole('button', { name: /^Expand/ }))
      const input = screen.getByRole('textbox', { name: 'User ID' })
      await userEvent.clear(input)
      await userEvent.type(input, '43{Enter}')
    } else {
      await userEvent.click(
        screen.getByRole('button', { name: 'Switch scope' })
      )
    }
    await waitFor(() => {
      expect(screen.queryByText('previous-token')).not.toBeInTheDocument()
      expect(screen.queryByText('17')).not.toBeInTheDocument()
    })
    completeRequest()
    expect(await screen.findByText('next-token')).toBeVisible()
    expect(await screen.findByText('23')).toBeVisible()
  }
)

it.each(['9007199254740993', '42&userId=43'])(
  'rejects ambiguous raw URL userId=%s before loading any log data',
  async (rawUserId) => {
    const { get } = await renderLogs(
      `/usage-logs/common?userId=${rawUserId}`,
      10,
      true
    )
    expect(screen.getByRole('alert')).toBeVisible()
    expect(
      screen.queryByRole('combobox', { name: 'Type' })
    ).not.toBeInTheDocument()
    expect(get).not.toHaveBeenCalled()
  }
)

it.each([
  ['1e2', '100'],
  ['42.0', '42'],
])(
  'normalizes safe numeric URL userId=%s to exact decimal ID %s',
  async (rawUserId, expectedId) => {
    const { get } = await renderLogs(`/usage-logs/common?userId=${rawUserId}`)
    await userEvent.click(screen.getByRole('button', { name: /^Expand/ }))
    expect(screen.getByRole('textbox', { name: 'User ID' })).toHaveValue(
      expectedId
    )
    const request = get.mock.calls
      .map(([url]) => new URL(url, 'http://localhost'))
      .find((url) => url.pathname === '/api/log')
    expect(request?.searchParams.get('user_id')).toBe(expectedId)
  }
)

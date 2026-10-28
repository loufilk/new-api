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
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import {
  SidebarProvider,
  SidebarMenu,
  Sidebar,
  SidebarTrigger,
} from '@/components/ui/sidebar'
import { api } from '@/lib/api'
import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'

import { ChatPresetsItem } from '../chat-presets-item'

const key = (id: number, name: string, status = 1) => ({
  id,
  name,
  status,
  key: 'masked',
  unlimited_quota: true,
  expired_time: -1,
  remain_quota: 0,
  used_quota: 0,
  created_time: 1,
  accessed_time: 1,
  group: 'default',
  auto_groups: [],
  cross_group_retry: false,
  model_limits_enabled: false,
  model_limits: '',
  allow_ips: '',
})
let client: QueryClient
const desktopWidth = window.innerWidth

beforeEach(() => {
  localStorage.clear()
  useAuthStore.getState().auth.setUser({ id: 7, username: 'fixture', role: 1 })
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  client.setQueryData(STATUS_QUERY_KEY, {
    server_address: 'https://api.example.com',
    chats: [
      { AionUI: 'aionui://provider/add?v=1&data={aionuiConfig}' },
      { 'CC Switch': 'ccswitch' },
      { 'Web Client': 'https://client.example.com/?key={key}&base={address}' },
    ],
    api_info: [
      {
        route: 'Asia',
        url: 'https://asia.example.com/proxy/v1/',
        description: '',
        color: 'blue',
      },
    ],
  })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url.startsWith('/api/token/?')) {
      return {
        data: {
          success: true,
          data: {
            items: [
              key(1, 'Disabled', 2),
              key(2, 'Development'),
              key(3, 'Production'),
            ],
            total: 3,
          },
        },
      }
    }
    if (url === '/api/token/3') {
      return { data: { success: true, data: key(3, 'Production') } }
    }
    throw new Error(`Unexpected fixture GET ${url}`)
  })
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { key: 'fixture-only' } },
  })
  vi.spyOn(window, 'open').mockReturnValue(null)
})

afterEach(() => {
  window.innerWidth = desktopWidth
  client.clear()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
})

async function openPreset(collapsed = false, name = 'AionUI', mobile = false) {
  const root = createRootRoute({
    component: () => (
      <SidebarProvider defaultOpen={!collapsed}>
        <SidebarTrigger />
        <Sidebar collapsible='icon'>
          <SidebarMenu>
            <ChatPresetsItem
              item={{ title: 'Third-party', type: 'chat-presets' }}
            />
          </SidebarMenu>
        </Sidebar>
      </SidebarProvider>
    ),
  })
  const route = createRoute({ getParentRoute: () => root, path: '/' })
  const router = createRouter({
    routeTree: root.addChildren([route]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  if (mobile) {
    await user.click(
      await screen.findByRole('button', { name: 'Toggle Sidebar' })
    )
  }
  await user.click(await screen.findByRole('button', { name: 'Third-party' }))
  await user.click(await screen.findByText(name))
  return user
}

it.each([false, true])(
  'asks for a key before retrieving a secret in collapsed=%s navigation',
  async (collapsed) => {
    await openPreset(collapsed)
    expect(
      await screen.findByRole(
        'dialog',
        {
          name: 'Select an API Key for AionUI',
        },
        { timeout: 10000 }
      )
    ).toBeVisible()
    expect(api.post).not.toHaveBeenCalled()
    expect(window.open).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Continue' })).toBeDisabled()
  }
)

it('keeps the mobile import dialog above the sidebar and closes both on cancellation', async () => {
  window.innerWidth = 390
  const user = await openPreset(false, 'AionUI', true)
  const dialog = await screen.findByRole(
    'dialog',
    { name: 'Select an API Key for AionUI' },
    { timeout: 10000 }
  )
  const sidebar = document.querySelector('[data-sidebar="sidebar"]')
  // Nested dialog portals share the modal layer and appear after the sidebar;
  // a raised sidebar layer would cover the key selector and its action buttons.
  expect(sidebar).toHaveClass('z-50')
  expect(dialog).toBeVisible()
  await user.click(screen.getByRole('combobox', { name: 'API Key' }))
  await user.click(screen.getByRole('option', { name: /Production/ }))
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(api.post).not.toHaveBeenCalled()
})

it('imports only the selected enabled key and selected endpoint after a second user action', async () => {
  const user = await openPreset()
  const combo = await screen.findByRole('combobox', { name: 'API Key' })
  await user.click(combo)
  expect(
    screen.queryByRole('option', { name: /Disabled/ })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('option', { name: /Production/ }))
  await user.click(screen.getByRole('button', { name: 'Continue' }))
  const endpoint = await screen.findByRole('combobox', { name: 'API Endpoint' })
  expect(api.post).toHaveBeenCalledWith(
    '/api/token/3/key',
    undefined,
    expect.objectContaining({ signal: expect.any(AbortSignal) })
  )
  expect(window.open).not.toHaveBeenCalled()
  await user.click(endpoint)
  await user.click(screen.getByRole('option', { name: /Asia/ }))
  await user.click(screen.getByRole('button', { name: 'Import to AionUI' }))
  const call = vi.mocked(window.open).mock.calls[0]
  const payload = JSON.parse(
    atob(new URL(call[0] as string).searchParams.get('data') ?? '')
  )
  expect(payload).toEqual({
    platform: 'new-api',
    apiKey: 'sk-fixture-only',
    baseUrl: 'https://asia.example.com/proxy',
  })
  expect(call.slice(1)).toEqual(['_blank', 'noopener,noreferrer'])
  expect(
    JSON.stringify(
      client
        .getQueryCache()
        .getAll()
        .map((q) => q.state.data)
    )
  ).not.toContain('fixture-only')
  expect(JSON.stringify({ ...localStorage })).not.toContain('fixture-only')
})

it('does not open a client when the user closes a pending key request', async () => {
  let finish!: (value: {
    data: { success: boolean; data: { key: string } }
  }) => void
  vi.mocked(api.post).mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  const user = await openPreset()
  await user.click(await screen.findByRole('combobox', { name: 'API Key' }))
  await user.click(screen.getByRole('option', { name: /Production/ }))
  await user.click(screen.getByRole('button', { name: 'Continue' }))
  await waitFor(() => expect(api.post).toHaveBeenCalled())
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await act(async () =>
    finish({ data: { success: true, data: { key: 'fixture-only' } } })
  )
  expect(window.open).not.toHaveBeenCalled()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it('can load an enabled key beyond the first 50 records', async () => {
  vi.mocked(api.get).mockImplementation(async (url) => ({
    data: {
      success: true,
      data: {
        items: url.includes('p=2')
          ? [key(51, 'Later key')]
          : [key(1, 'Disabled', 2)],
        total: 51,
      },
    },
  }))
  const user = await openPreset()
  expect(await screen.findByText('No available API Keys')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Load more' }))
  await user.click(screen.getByRole('combobox', { name: 'API Key' }))
  await user.click(await screen.findByRole('option', { name: /Later key/ }))
  expect(screen.getByRole('button', { name: 'Continue' })).toBeEnabled()
  expect(api.post).not.toHaveBeenCalled()
})

it('does not reveal a key disabled after the list was loaded', async () => {
  const user = await openPreset()
  await user.click(await screen.findByRole('combobox', { name: 'API Key' }))
  await user.click(screen.getByRole('option', { name: /Production/ }))
  vi.mocked(api.get).mockResolvedValue({
    data: { success: true, data: key(3, 'Production', 2) },
  })
  await user.click(screen.getByRole('button', { name: 'Continue' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Continue' })).toBeEnabled()
  )
  expect(api.post).not.toHaveBeenCalled()
  expect(window.open).not.toHaveBeenCalled()
  expect(
    screen.getByRole('dialog', { name: 'Select an API Key for AionUI' })
  ).toBeVisible()
})

it('allows retry after metadata loading fails without reading a secret', async () => {
  vi.mocked(api.get).mockRejectedValueOnce(new Error('fixture unavailable'))
  const user = await openPreset()
  await user.click(await screen.findByRole('button', { name: 'Retry' }))
  expect(await screen.findByRole('combobox', { name: 'API Key' })).toBeVisible()
  expect(api.post).not.toHaveBeenCalled()
})

it('uses the same explicit key selection for web clients', async () => {
  await openPreset(false, 'Web Client')
  expect(
    await screen.findByRole('dialog', {
      name: 'Select an API Key for Web Client',
    })
  ).toBeVisible()
  expect(window.open).not.toHaveBeenCalled()
  expect(api.post).not.toHaveBeenCalled()
})

it('drops the import attempt when the account signs out', async () => {
  await openPreset()
  await screen.findByRole('dialog')
  act(() => useAuthStore.getState().auth.reset())
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  act(() =>
    useAuthStore
      .getState()
      .auth.setUser({ id: 7, username: 'fixture', role: 1 })
  )
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(window.open).not.toHaveBeenCalled()
})

it('reuses key creation and reloads the list when a new key is created', async () => {
  const oldGet = vi.mocked(api.get).getMockImplementation()
  let created = false
  vi.mocked(api.get).mockImplementation(async (url, config) => {
    if (url === '/api/user/self/groups') {
      return {
        data: {
          success: true,
          data: { default: { desc: 'Default', ratio: 1 } },
        },
      }
    }
    if (url === '/api/token/auto-groups') {
      return { data: { success: true, data: { groups: [], max_count: 5 } } }
    }
    if (created && url.startsWith('/api/token/?')) {
      return {
        data: {
          success: true,
          data: { items: [key(4, 'New client key')], total: 1 },
        },
      }
    }
    if (!oldGet) throw new Error('Missing fixture handler')
    return oldGet(url, config)
  })
  vi.mocked(api.post).mockImplementation(async (url) => {
    if (url === '/api/token/models') {
      return { data: { success: true, data: [] } }
    }
    if (url === '/api/token/') {
      created = true
      return { data: { success: true } }
    }
    throw new Error('Unexpected key reveal')
  })
  const user = await openPreset()
  await user.click(
    await screen.findByRole('button', { name: 'Create new Key' })
  )
  await user.type(
    await screen.findByRole('textbox', { name: /^Name/ }),
    'New client key'
  )
  await user.click(screen.getByRole('button', { name: /Save/ }))
  const combo = await screen.findByRole('combobox', { name: 'API Key' })
  await user.click(combo)
  expect(
    await screen.findByRole('option', { name: /New client key/ })
  ).toBeVisible()
  expect(window.open).not.toHaveBeenCalled()
})

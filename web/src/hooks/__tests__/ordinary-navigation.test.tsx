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
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { useSidebarView } from '../use-sidebar-view'

function Navigation() {
  const { navGroups } = useSidebarView()
  return (
    <nav>
      {navGroups
        .flatMap((group) => group.items)
        .map(
          (item) =>
            'url' in item && (
              <a key={item.title} href={item.url}>
                {item.title}
              </a>
            )
        )}
    </nav>
  )
}

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
})

async function renderNavigation(
  role: number,
  accountPoolEnabled = false,
  modelTestEnabled: unknown = false,
  path = '/wallet',
  sidebar: { admin?: object; user?: object } = {}
) {
  useAuthStore.getState().auth.setUser({
    id: 7,
    username: 'user',
    role,
    sidebar_modules: sidebar.user ? JSON.stringify(sidebar.user) : '',
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], {
    SidebarModulesAdmin: sidebar.admin ? JSON.stringify(sidebar.admin) : '',
    account_pool_enabled: accountPoolEnabled,
    model_test_enabled: modelTestEnabled,
  })
  const router = createRouter({
    routeTree: createRootRoute({ component: Navigation }),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

it.each([1, 5])(
  'role %s sees the console entries with native text and image generation',
  async (role) => {
    await renderNavigation(role)
    expect(await screen.findByRole('link', { name: 'Wallet' })).toHaveAttribute(
      'href',
      '/wallet'
    )
    expect(screen.getAllByRole('link').map((link) => link.textContent)).toEqual(
      [
        'Overview',
        'Dashboard',
        'API Keys',
        'Usage Logs',
        'Audit Logs',
        'Channel status',
        'Task Logs',
        'System announcements',
        'Text Generation',
        'Image Generation',
        'Wallet',
        'Invitation Rewards',
        'Invoices',
        'Partner application',
        'My tickets',
        'Profile',
        'Security & Access',
      ]
    )
  }
)

it('administrators retain normal redemption and user management but no reseller workspace', async () => {
  await renderNavigation(ROLE.ADMIN, false, false, '/channels')
  expect(
    await screen.findByRole('link', { name: 'Redemption Codes' })
  ).toHaveAttribute('href', '/redemption-codes')
  expect(screen.getByRole('link', { name: 'Users' })).toHaveAttribute(
    'href',
    '/users'
  )
  expect(
    screen
      .getAllByRole('link')
      .map((link) => link.getAttribute('href'))
      .filter((href) => href?.startsWith('/agent-'))
  ).toEqual([])
})

it.each([1, 5, 10, 100])(
  'role %s keeps extra tools out of console navigation when their services are enabled',
  async (role) => {
    await renderNavigation(role, true, true)
    await screen.findByRole('link', { name: 'Wallet' })
    const links = screen
      .getAllByRole('link')
      .map((link) => link.getAttribute('href'))
    expect(links).toContain('/playground')
    expect(links).toContain('/image')
    for (const extra of [
      '/account-pool',
      '/intelligence',
      '/getting-started',
      '/dashboard/flow',
      '/pricing',
      '/rankings',
      '/help',
      '/agent-platform',
      '/redemption-codes?view=center',
      '/redemption-codes?view=records',
    ]) {
      expect(links).not.toContain(extra)
    }
  }
)

it.each([
  {
    condition: 'administrator disables images',
    admin: { chat: { enabled: true, image: false } },
    user: undefined,
    playgroundVisible: true,
    imageVisible: false,
  },
  {
    condition: 'user disables text generation',
    admin: undefined,
    user: { chat: { enabled: true, playground: false } },
    playgroundVisible: false,
    imageVisible: true,
  },
  {
    condition: 'administrator disables the chat section despite user settings',
    admin: { chat: { enabled: false } },
    user: { chat: { enabled: true, playground: true, image: true } },
    playgroundVisible: false,
    imageVisible: false,
  },
  {
    condition: 'user disables the chat section',
    admin: undefined,
    user: { chat: { enabled: false } },
    playgroundVisible: false,
    imageVisible: false,
  },
])(
  '$condition preserves the corresponding generation menu visibility',
  async (sidebar) => {
    await renderNavigation(ROLE.USER, false, false, '/wallet', sidebar)
    await screen.findByRole('link', { name: 'Wallet' })
    expect(
      Boolean(screen.queryByRole('link', { name: 'Text Generation' }))
    ).toBe(sidebar.playgroundVisible)
    expect(
      Boolean(screen.queryByRole('link', { name: 'Image Generation' }))
    ).toBe(sidebar.imageVisible)
  }
)

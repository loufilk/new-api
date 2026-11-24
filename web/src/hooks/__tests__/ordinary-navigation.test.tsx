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
import {
  focusManager,
  QueryClient,
  QueryClientProvider,
} from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
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
  focusManager.setFocused(undefined)
  useAuthStore.getState().auth.reset()
})

async function renderNavigation(
  role: number,
  accountPoolEnabled = false,
  modelTestEnabled: unknown = false,
  invoiceCenterVisible?: boolean,
  invoiceEnabled = false
) {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'user', role })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], {
    SidebarModulesAdmin: '',
    account_pool_enabled: accountPoolEnabled,
    model_test_enabled: modelTestEnabled,
    customer_invoice_enabled: invoiceEnabled,
    ...(invoiceCenterVisible === undefined
      ? {}
      : { customer_invoice_center_visible: invoiceCenterVisible }),
  })
  const router = createRouter({
    routeTree: createRootRoute({ component: Navigation }),
    history: createMemoryHistory({ initialEntries: ['/wallet'] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

it.each([1, 5])(
  'role %s retains ordinary navigation and only its authorized A07 workspace',
  async (role) => {
    await renderNavigation(role)
    expect(await screen.findByRole('link', { name: 'Wallet' })).toHaveAttribute(
      'href',
      '/wallet'
    )
    for (const name of [
      'API Keys',
      'Text Generation',
      'Image Generation',
      'Profile',
      'Security & Access',
    ]) {
      expect(screen.getByRole('link', { name })).toBeInTheDocument()
    }
    expect(
      screen.queryByRole('link', { name: 'Redemption Codes' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: 'Users' })
    ).not.toBeInTheDocument()
    expect(
      screen
        .getAllByRole('link')
        .map((link) => link.getAttribute('href'))
        .filter((href) => href?.startsWith('/agent-'))
    ).toEqual(role === 5 ? ['/agent-platform'] : [])
    for (const view of ['center', 'records']) {
      const codes = screen
        .getAllByRole('link')
        .filter(
          (link) =>
            link.getAttribute('href') === `/redemption-codes?view=${view}`
        )
      expect(codes).toHaveLength(role === 5 ? 1 : 0)
    }
    expect(
      screen
        .getAllByRole('link')
        .some((link) => link.getAttribute('href') === '/agent-platform/finance')
    ).toBe(false)
  }
)

it('administrators retain normal redemption and user management but no reseller workspace', async () => {
  await renderNavigation(ROLE.ADMIN)
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
  'role %s sees the account pool entry only when enabled',
  async (role) => {
    await renderNavigation(role)
    expect(
      screen
        .getAllByRole('link')
        .some((link) => link.getAttribute('href') === '/account-pool')
    ).toBe(false)
    cleanup()
    await renderNavigation(role, true)
    expect(
      screen
        .getAllByRole('link')
        .filter((link) => link.getAttribute('href') === '/account-pool')
    ).toHaveLength(1)
  }
)

it('shows the invoice center by default with the new visibility status flag', async () => {
  await renderNavigation(ROLE.USER, false, false, true, false)
  expect(
    await screen.findByRole('link', { name: 'Invoice center' })
  ).toHaveAttribute('href', '/invoice')
})

it('uses the intake status as the visibility fallback for older servers', async () => {
  await renderNavigation(ROLE.USER, false, false, undefined, true)
  expect(
    await screen.findByRole('link', { name: 'Invoice center' })
  ).toHaveAttribute('href', '/invoice')
})

it('respects an explicit invoice center visibility disablement', async () => {
  await renderNavigation(ROLE.USER, false, false, false, true)
  expect(screen.queryByRole('link', { name: 'Invoice center' })).toBeNull()
})

it('refreshes fresh cached invoice visibility when the ordinary customer returns to the tab', async () => {
  const status = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        customer_invoice_center_visible: false,
        customer_invoice_enabled: false,
      },
    },
  })
  await renderNavigation(ROLE.USER, false, false, true, false)
  expect(
    await screen.findByRole('link', { name: 'Invoice center' })
  ).toBeInTheDocument()
  expect(status).not.toHaveBeenCalled()

  act(() => focusManager.setFocused(false))
  act(() => focusManager.setFocused(true))
  await waitFor(() =>
    expect(
      screen.queryByRole('link', { name: 'Invoice center' })
    ).not.toBeInTheDocument()
  )
  expect(status).toHaveBeenCalledExactlyOnceWith('/api/status')
})

it.each([1, 5, 10, 100])(
  'role %s never sees the retired model monitor but keeps playground',
  async (role) => {
    await renderNavigation(role, true, 'true')
    expect(
      screen
        .getAllByRole('link')
        .some((link) => link.getAttribute('href') === '/intelligence')
    ).toBe(false)
    expect(
      screen.getByRole('link', { name: 'Text Generation' })
    ).toHaveAttribute('href', '/playground')
    cleanup()
    await renderNavigation(role, false, true)
    expect(
      screen
        .getAllByRole('link')
        .filter((link) => link.getAttribute('href') === '/intelligence')
    ).toHaveLength(0)
    expect(
      screen
        .getAllByRole('link')
        .some((link) => link.getAttribute('href') === '/account-pool')
    ).toBe(false)
  }
)

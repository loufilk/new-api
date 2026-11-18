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
  Link,
  RouterProvider,
} from '@tanstack/react-router'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { SidebarViewHeader } from '@/components/layout/components/sidebar-view-header'
import { SidebarProvider } from '@/components/ui/sidebar'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { useSidebarView } from '../use-sidebar-view'

function Navigation() {
  const { view, navGroups } = useSidebarView()
  return (
    <SidebarProvider>
      {view && <SidebarViewHeader view={view} />}
      <nav aria-label='Workspace'>
        {navGroups.map((group) => (
          <section key={group.id}>
            <h2>{group.title}</h2>
            {group.items.map((item) =>
              item.url ? (
                <Link key={item.title} to={item.url}>
                  {item.title}
                </Link>
              ) : null
            )}
          </section>
        ))}
      </nav>
    </SidebarProvider>
  )
}

beforeEach(() => {
  window.localStorage.clear()
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [] },
  })
})

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

async function renderNavigation(role: number, path: string) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], { SidebarModulesAdmin: '' })
  const root = createRootRoute({ component: Navigation })
  const routes = [
    '/dashboard/overview',
    '/channels',
    '/model-management/deployments',
    '/system-settings/site',
  ].map((routePath) =>
    createRoute({ getParentRoute: () => root, path: routePath })
  )
  const router = createRouter({
    routeTree: root.addChildren(routes),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return router
}

it.each([ROLE.ADMIN, ROLE.SUPER_ADMIN])(
  'role %s sees only the fifteen user entries on the overview',
  async (role) => {
    await renderNavigation(role, '/dashboard/overview')
    const workspace = await screen.findByRole('navigation', {
      name: 'Workspace',
    })
    expect(
      within(workspace)
        .getAllByRole('heading')
        .map((item) => item.textContent)
    ).toEqual(['General', 'Personal'])
    expect(within(workspace).getAllByRole('link')).toHaveLength(15)
    expect(
      within(workspace).queryByRole('link', { name: 'Channels' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: 'Back to Dashboard' })
    ).not.toBeInTheDocument()
  }
)

it.each(['/channels', '/model-management/deployments'])(
  '%s shows administration navigation and returns to the user console',
  async (path) => {
    const router = await renderNavigation(ROLE.SUPER_ADMIN, path)
    const workspace = await screen.findByRole('navigation', {
      name: 'Workspace',
    })
    expect(
      within(workspace).getByRole('heading', { name: 'Admin' })
    ).toBeVisible()
    expect(
      within(workspace).getByRole('link', { name: 'Channels' })
    ).toBeVisible()
    expect(
      within(workspace).queryByRole('link', { name: 'Wallet' })
    ).not.toBeInTheDocument()
    await userEvent.click(
      screen.getByRole('link', { name: 'Back to Dashboard' })
    )
    expect(await screen.findByRole('link', { name: 'Wallet' })).toBeVisible()
    expect(router.state.location.pathname).toBe('/dashboard/overview')
    expect(
      screen.queryByRole('heading', { name: 'Admin' })
    ).not.toBeInTheDocument()
  }
)

it('keeps the system settings subview and its return to the console', async () => {
  const router = await renderNavigation(
    ROLE.SUPER_ADMIN,
    '/system-settings/site'
  )
  expect(
    await screen.findByRole('heading', { name: 'System Administration' })
  ).toBeVisible()
  expect(
    screen.queryByRole('heading', { name: 'Admin' })
  ).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('link', { name: 'Back to Dashboard' }))
  expect(await screen.findByRole('link', { name: 'Overview' })).toBeVisible()
  expect(router.state.location.pathname).toBe('/dashboard/overview')
})

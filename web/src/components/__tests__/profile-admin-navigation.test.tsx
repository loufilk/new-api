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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it } from 'vitest'

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { ProfileDropdown } from '../profile-dropdown'

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
})

async function openProfile(options: {
  role: number
  adminConfig?: Record<string, boolean>
  userConfig?: Record<string, boolean>
}) {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'alice',
    role: options.role,
    sidebar_modules: options.userConfig
      ? JSON.stringify({ admin: options.userConfig })
      : '',
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], {
    SidebarModulesAdmin: options.adminConfig
      ? JSON.stringify({ admin: options.adminConfig })
      : '',
  })
  const root = createRootRoute({ component: ProfileDropdown })
  const routes = [
    '/dashboard/overview',
    '/channels',
    '/model-management/metadata',
    '/users',
  ].map((path) => createRoute({ getParentRoute: () => root, path }))
  const router = createRouter({
    routeTree: root.addChildren(routes),
    history: createMemoryHistory({ initialEntries: ['/dashboard/overview'] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await userEvent.click(await screen.findByRole('button'))
  await screen.findByRole('menuitem', { name: 'Profile' })
  return router
}

it.each([ROLE.USER, 5])(
  'role %s has no administration entry in the profile menu',
  async (role) => {
    await openProfile({ role })
    expect(
      screen.queryByRole('menuitem', { name: 'Administration' })
    ).not.toBeInTheDocument()
  }
)

it.each([ROLE.ADMIN, ROLE.SUPER_ADMIN])(
  'role %s enters the first visible administration page from the profile menu',
  async (role) => {
    const router = await openProfile({ role })
    await userEvent.click(
      screen.getByRole('menuitem', { name: 'Administration' })
    )
    await waitFor(() =>
      expect(router.state.location.pathname).toBe('/channels')
    )
  }
)

it('skips administration pages hidden by site and personal navigation settings', async () => {
  const router = await openProfile({
    role: ROLE.ADMIN,
    adminConfig: { enabled: true, channel: false },
    userConfig: { enabled: true, models: false },
  })
  await userEvent.click(
    screen.getByRole('menuitem', { name: 'Administration' })
  )
  await waitFor(() => expect(router.state.location.pathname).toBe('/users'))
})

it('omits the administration entry when no permitted administration page is visible', async () => {
  await openProfile({ role: ROLE.ADMIN, adminConfig: { enabled: false } })
  expect(
    screen.queryByRole('menuitem', { name: 'Administration' })
  ).not.toBeInTheDocument()
})

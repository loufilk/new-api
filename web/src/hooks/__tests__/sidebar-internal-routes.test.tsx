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
import { render, renderHook, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { NavGroup } from '@/components/layout/components/nav-group'
import { SidebarProvider } from '@/components/ui/sidebar'
import { useSidebarData } from '@/hooks/use-sidebar-data'

function SidebarTestLayout() {
  const sidebar = useSidebarData()
  const general = sidebar.navGroups.find((group) => group.id === 'general')
  const items = (general?.items ?? []).filter(
    (item) =>
      'url' in item &&
      (item.url === '/keys' || item.url === '/usage-logs/common')
  )
  return (
    <SidebarProvider>
      <NavGroup title={general?.title ?? 'General'} items={items} />
      <Outlet />
    </SidebarProvider>
  )
}

function createSidebarTestRouter() {
  const rootRoute = createRootRoute({
    component: SidebarTestLayout,
  })
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: () => <div>home</div>,
  })
  const keysRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/keys',
    component: () => <div>keys</div>,
  })
  const logsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/usage-logs/common',
    component: () => <div>logs</div>,
  })
  const routeTree = rootRoute.addChildren([indexRoute, keysRoute, logsRoute])
  return createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
}

describe('console sidebar links stay inside the SPA router', () => {
  test.each([false, true])(
    'both entries remain internal when account pool enabled=%s',
    (accountPoolEnabled) => {
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false } },
      })
      queryClient.setQueryData(['status'], {
        account_pool_enabled: accountPoolEnabled,
      })
      const { result, unmount } = renderHook(() => useSidebarData(), {
        wrapper: (props) => (
          <QueryClientProvider client={queryClient}>
            {props.children}
          </QueryClientProvider>
        ),
      })
      const general = result.current.navGroups.find(
        (group) => group.id === 'general'
      )
      expect(general).toBeDefined()

      for (const url of ['/keys', '/usage-logs/common']) {
        const item = (general?.items ?? []).find(
          (entry) => 'url' in entry && entry.url === url
        ) as { url: string; external?: boolean } | undefined
        expect(item).toBeDefined()
        expect(item?.url.startsWith('/')).toBe(true)
        expect(item?.external).toBeFalsy()
      }

      expect(
        (general?.items ?? [])
          .filter((item) => 'url' in item)
          .map((item) => (item as { url: string }).url)
      ).toEqual(expect.arrayContaining(['/keys', '/usage-logs/common']))
      unmount()
      queryClient.clear()
    }
  )

  test('clicking keys and logs navigates via the router without a document reload', async () => {
    const user = userEvent.setup()
    const router = createSidebarTestRouter()
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    queryClient.setQueryData(['status'], { account_pool_enabled: false })
    await router.load()

    const beforeHref = window.location.href
    render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    )

    for (const url of ['/keys', '/usage-logs/common']) {
      const link = screen.getByRole('link', {
        name: url === '/keys' ? /API Keys/i : /Usage Logs/i,
      })
      // Internal SPA links must not open a new document or tab.
      expect(link.getAttribute('href')).toBe(url)
      expect(link.getAttribute('target')).toBeNull()

      await user.click(link)
      expect(router.state.location.pathname).toBe(url)
      // Memory-history SPA navigation must not trigger a full document load.
      expect(window.location.href).toBe(beforeHref)
    }
    queryClient.clear()
  })
})

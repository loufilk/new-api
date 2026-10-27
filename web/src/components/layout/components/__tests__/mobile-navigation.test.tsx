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
import { afterEach, describe, expect, test, vi } from 'vitest'

import { SidebarProvider, useSidebar } from '@/components/ui/sidebar'

import { AppHeader } from '../app-header'

// jsdom does not implement pointer capture, which vaul calls on pointer down.
for (const [name, value] of [
  ['setPointerCapture', () => undefined],
  ['releasePointerCapture', () => undefined],
  ['hasPointerCapture', () => false],
] as const) {
  if (typeof HTMLElement.prototype[name] !== 'function') {
    Object.defineProperty(HTMLElement.prototype, name, {
      configurable: true,
      value,
    })
  }
}

vi.mock('@/components/search', () => ({ Search: () => null }))
vi.mock('@/components/config-drawer', () => ({ ConfigDrawer: () => null }))
vi.mock('@/components/notification-popover', () => ({
  NotificationPopover: () => null,
}))
vi.mock('@/components/profile-dropdown', () => ({
  ProfileDropdown: () => null,
}))
vi.mock('@/features/system-update/system-update-action', () => ({
  SystemUpdateAction: () => null,
}))
vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({ status: null }),
}))
vi.mock('@/hooks/use-system-config', () => ({
  useSystemConfig: () => ({
    systemName: 'New API',
    logo: '/logo.png',
    loading: false,
    logoLoaded: true,
  }),
}))

const originalInnerWidth = window.innerWidth
const queryClients: QueryClient[] = []

function SidebarStateProbe() {
  const { openMobile } = useSidebar()
  return <output data-testid='sidebar-mobile-open'>{String(openMobile)}</output>
}

async function renderHeader(showTopNav = true) {
  const rootRoute = createRootRoute({
    component: () => (
      <SidebarProvider>
        <AppHeader showTopNav={showTopNav} />
        <SidebarStateProbe />
      </SidebarProvider>
    ),
  })
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: () => <div>home</div>,
  })
  const signInRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/sign-in',
    validateSearch: (search: Record<string, unknown>) => ({
      redirect:
        typeof search.redirect === 'string' ? search.redirect : undefined,
    }),
    component: () => <div>sign in</div>,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, signInRoute]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  queryClient.setQueryData(['notice'], { success: true, data: '' })
  queryClients.push(queryClient)
  await router.load()
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

function setViewportWidth(width: number) {
  Object.defineProperty(window, 'innerWidth', {
    configurable: true,
    writable: true,
    value: width,
  })
}

afterEach(async () => {
  cleanup()
  for (const client of queryClients.splice(0)) {
    await client.cancelQueries()
    client.clear()
  }
  setViewportWidth(originalInnerWidth)
})

describe('AppHeader mobile navigation entry', () => {
  test('the entry is mobile-only and opens the navigation drawer', async () => {
    const user = userEvent.setup()
    await renderHeader()

    const trigger = screen.getByRole('button', { name: 'Open menu' })
    expect(trigger.className).toContain('lg:hidden')
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getByRole('navigation')).toBeInTheDocument()

    await user.click(trigger)

    const drawer = await screen.findByRole('dialog')
    expect(within(drawer).getByRole('link', { name: 'Home' })).toBeVisible()
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    // The desktop navigation stays in the document for large viewports.
    expect(document.querySelector('nav')).toBeInTheDocument()
  })

  test('the drawer keeps focus inside while open and restores it on Escape', async () => {
    const user = userEvent.setup()
    await renderHeader()

    const trigger = screen.getByRole('button', { name: 'Open menu' })
    await user.click(trigger)

    const drawer = await screen.findByRole('dialog')
    await waitFor(() =>
      expect(drawer.contains(document.activeElement)).toBe(true)
    )

    await user.keyboard('{Escape}')

    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(document.activeElement).toBe(trigger)
  })

  test('opening the console sidebar closes the mobile drawer', async () => {
    setViewportWidth(500)
    const user = userEvent.setup()
    await renderHeader()

    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await screen.findByRole('dialog')

    // The mobile sidebar opens through the provider's keyboard shortcut; the
    // drawer must yield so the two overlays can never stack.
    await user.keyboard('{Control>}b{/Control}')

    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(screen.getByTestId('sidebar-mobile-open')).toHaveTextContent('true')
    expect(screen.getByRole('button', { name: 'Open menu' })).toHaveAttribute(
      'aria-expanded',
      'false'
    )
  })

  test('hiding the top nav also hides the mobile entry and drawer', async () => {
    await renderHeader(false)

    expect(
      screen.queryByRole('button', { name: 'Open menu' })
    ).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})

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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState, type ReactNode } from 'react'
import { afterEach, describe, expect, test, vi } from 'vitest'

import type { AuthUser } from '@/stores/auth-store'

import { MobileDrawer, type MobileDrawerProps } from '../mobile-drawer'

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

const isSidebarModuleVisible = vi.hoisted(() => vi.fn(() => true))

vi.mock('@/hooks/use-sidebar-config', () => ({
  useIsSidebarModuleVisible: isSidebarModuleVisible,
}))

const user: AuthUser = {
  id: 7,
  username: 'mobile-user',
  role: 1,
}

const defaultProps: MobileDrawerProps = {
  isOpen: true,
  onClose: () => undefined,
  homeUrl: '/',
  displayLogo: null,
  displaySiteName: 'New API',
  loading: false,
  logoLoaded: true,
  mobileLinksList: [],
  showAuthButtons: true,
  user: null,
}

let drawerProps: MobileDrawerProps = defaultProps

function TestLayout() {
  return <MobileDrawer {...drawerProps} />
}

function renderLayout(Component: () => ReactNode) {
  const rootRoute = createRootRoute({ component: Component })
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: () => <div>home</div>,
  })
  const dashboardRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/dashboard',
    component: () => <div>dashboard</div>,
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
    routeTree: rootRoute.addChildren([indexRoute, dashboardRoute, signInRoute]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return router
}

function renderDrawer(overrides: Partial<MobileDrawerProps> = {}) {
  drawerProps = { ...defaultProps, ...overrides }
  return renderLayout(TestLayout)
}

function StatefulTestLayout() {
  const [isOpen, setIsOpen] = useState(true)
  return (
    <MobileDrawer
      {...drawerProps}
      isOpen={isOpen}
      onClose={() => {
        drawerProps.onClose()
        setIsOpen(false)
      }}
    />
  )
}

function renderDrawerWithCloseState(
  overrides: Partial<MobileDrawerProps> = {}
) {
  drawerProps = { ...defaultProps, ...overrides }
  return renderLayout(StatefulTestLayout)
}

/**
 * The document-level navigation "not implemented" notice is irrelevant to
 * these assertions; keep the browser from attempting a real document load.
 */
function preventDocumentNavigation(link: HTMLElement) {
  link.addEventListener('click', (event) => event.preventDefault(), true)
}

afterEach(() => {
  drawerProps = defaultProps
  isSidebarModuleVisible.mockReturnValue(true)
})

describe('mobile navigation drawer', () => {
  test('an internal link closes the drawer and keeps SPA navigation', async () => {
    const userEventApi = userEvent.setup()
    const onClose = vi.fn()
    const router = renderDrawer({
      onClose,
      mobileLinksList: [{ title: 'Console', href: '/dashboard' }],
    })

    const link = await screen.findByRole('link', { name: 'Console' })
    expect(link.getAttribute('href')).toBe('/dashboard')
    expect(link.getAttribute('target')).toBeNull()

    await userEventApi.click(link)

    expect(onClose).toHaveBeenCalledTimes(1)
    expect(router.state.location.pathname).toBe('/dashboard')
  })

  test('a documentNavigation link closes the drawer without opening a tab', async () => {
    const userEventApi = userEvent.setup()
    const onClose = vi.fn()
    renderDrawer({
      onClose,
      mobileLinksList: [
        { title: 'About', href: '/about', documentNavigation: true },
      ],
    })

    const link = await screen.findByRole('link', { name: 'About' })
    expect(link.getAttribute('href')).toBe('/about')
    expect(link.getAttribute('target')).toBeNull()
    preventDocumentNavigation(link)

    await userEventApi.click(link)

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  test('an external link closes the drawer and opens in a new tab', async () => {
    const userEventApi = userEvent.setup()
    const onClose = vi.fn()
    renderDrawer({
      onClose,
      mobileLinksList: [
        { title: 'Docs', href: 'https://docs.example.com', external: true },
      ],
    })

    const link = await screen.findByRole('link', { name: 'Docs' })
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
    preventDocumentNavigation(link)

    await userEventApi.click(link)

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  test('a disabled link is marked disabled and does not close the drawer', async () => {
    const userEventApi = userEvent.setup()
    const onClose = vi.fn()
    renderDrawer({
      onClose,
      mobileLinksList: [
        { title: 'Rankings', href: '/dashboard', disabled: true },
      ],
    })

    const link = await screen.findByRole('link', { name: 'Rankings' })
    expect(link.getAttribute('aria-disabled')).toBe('true')

    preventDocumentNavigation(link)
    await userEventApi.click(link)

    expect(onClose).not.toHaveBeenCalled()
  })

  test('an authentication-only link sends anonymous visitors to sign in', async () => {
    const userEventApi = userEvent.setup()
    const onClose = vi.fn()
    const router = renderDrawer({
      onClose,
      user: null,
      mobileLinksList: [
        { title: 'Rankings', href: '/dashboard', requiresAuth: true },
      ],
    })

    const link = await screen.findByRole('link', { name: 'Rankings' })

    await userEventApi.click(link)

    expect(onClose).toHaveBeenCalledTimes(1)
    expect(router.state.location.pathname).toBe('/sign-in')
    expect(router.state.location.search).toEqual({ redirect: '/dashboard' })
  })

  test('an authentication-only link stays on its route for signed-in users', async () => {
    const userEventApi = userEvent.setup()
    const onClose = vi.fn()
    const router = renderDrawer({
      onClose,
      user,
      mobileLinksList: [
        { title: 'Rankings', href: '/dashboard', requiresAuth: true },
      ],
    })

    const link = await screen.findByRole('link', { name: 'Rankings' })
    await userEventApi.click(link)

    expect(onClose).toHaveBeenCalledTimes(1)
    expect(router.state.location.pathname).toBe('/dashboard')
  })

  test('Escape closes the open drawer', async () => {
    const userEventApi = userEvent.setup()
    const onClose = vi.fn()
    renderDrawer({ onClose })

    await screen.findByRole('dialog')
    await userEventApi.keyboard('{Escape}')

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  test('the security entry follows the sidebar module visibility', async () => {
    isSidebarModuleVisible.mockReturnValue(false)
    renderDrawer({ user })

    expect(
      await screen.findByRole('link', { name: 'Profile' })
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: 'Security & Access' })
    ).not.toBeInTheDocument()
  })

  test('pressing Sign out closes the drawer before the confirmation opens', async () => {
    const userEventApi = userEvent.setup()
    const onClose = vi.fn()
    renderDrawerWithCloseState({ onClose, user })

    await userEventApi.click(
      await screen.findByRole('button', { name: 'Sign out' })
    )

    expect(onClose).toHaveBeenCalledTimes(1)
    const confirmDialog = await screen.findByRole('alertdialog')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(
      within(confirmDialog).getByRole('button', { name: 'Sign out' })
    ).toBeEnabled()
  })

  test('cancelling the confirmation dialog keeps the drawer closed', async () => {
    const userEventApi = userEvent.setup()
    const onClose = vi.fn()
    renderDrawerWithCloseState({ onClose, user })

    await userEventApi.click(
      await screen.findByRole('button', { name: 'Sign out' })
    )
    const confirmDialog = await screen.findByRole('alertdialog')

    await userEventApi.click(
      within(confirmDialog).getByRole('button', { name: 'Cancel' })
    )

    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    )
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})

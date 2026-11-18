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
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import '@/extensions/bootstrap'
import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { PublicHeader } from '../public-header'

const queryClients: QueryClient[] = []
let notifyResize: ResizeObserverCallback | undefined
let observedHeader: Element | undefined
const initialRootFontSize = document.documentElement.style.fontSize

beforeEach(() => {
  document.documentElement.style.fontSize = '16px'
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(callback: ResizeObserverCallback) {
        notifyResize = callback
      }
      observe(element: Element) {
        observedHeader = element
      }
      unobserve() {}
      disconnect() {}
    }
  )
  useSystemConfigStore.getState().setLoading(false)
})

afterEach(async () => {
  cleanup()
  for (const client of queryClients.splice(0)) {
    await client.cancelQueries()
    client.clear()
  }
  document.documentElement.style.fontSize = initialRootFontSize
  i18next.removeResourceBundle('fr', 'translation')
  void i18next.changeLanguage('en')
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  notifyResize = undefined
  observedHeader = undefined
  useSystemConfigStore.getState().setLoading(true)
})

async function renderHeader(
  requiresAuth = false,
  initialPath = '/',
  allModules = false
) {
  const rootRoute = createRootRoute({
    component: () => (
      <PublicHeader
        showNotifications={false}
        showThemeSwitch={false}
        showLanguageSwitcher={false}
      />
    ),
  })
  const routes = [
    '/',
    '/help',
    '/sign-in',
    '/pricing',
    '/health',
    '/partner',
    '/rankings',
    '/contact',
    '/affiliate',
    '/system-announcements',
  ].map((path) => createRoute({ getParentRoute: () => rootRoute, path }))
  const router = createRouter({
    routeTree: rootRoute.addChildren(routes),
    history: createMemoryHistory({ initialEntries: [initialPath] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  queryClients.push(client)
  client.setQueryData(STATUS_QUERY_KEY, {
    account_pool_enabled: allModules,
    HeaderNavModules: JSON.stringify({
      home: true,
      console: allModules,
      pricing: {
        enabled: requiresAuth || allModules,
        requireAuth: requiresAuth,
      },
      rankings: { enabled: allModules },
      docs: true,
      about: allModules,
    }),
  })
  client.setQueryData(['notice'], { success: true, data: '' })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return router
}

describe('public header responsive navigation', () => {
  test('keeps home navigation and switches to the reference menu when entering the model square', async () => {
    const router = await renderHeader(false, '/', true)
    const banner = screen.getByRole('banner')
    expect(
      within(banner).getByRole('link', { name: 'API Access' })
    ).toBeVisible()
    expect(within(banner).getByRole('link', { name: 'About' })).toBeVisible()
    expect(
      within(banner)
        .getAllByRole('link')
        .some((link) => link.getAttribute('href') === '/account-pool')
    ).toBe(true)
    expect(
      within(banner).queryByRole('button', { name: 'Service Center' })
    ).not.toBeInTheDocument()

    await act(() => router.navigate({ to: '/pricing' }))
    const desktop = within(banner).getByRole('link', {
      name: 'Home',
    }).parentElement
    if (!desktop) throw new Error('Desktop navigation container is missing')
    expect(
      within(desktop)
        .getAllByRole('link')
        .map((link) => link.textContent)
        .filter((name) => name !== 'Sign in')
    ).toEqual([
      'Home',
      'Console',
      'Model Square',
      'Channel status',
      'Partner application',
      'Rankings',
      'Docs',
    ])
    expect(
      within(desktop).getByRole('button', { name: 'Service Center' })
    ).toBeVisible()
    const urls = within(desktop)
      .getAllByRole('link')
      .map((link) => link.getAttribute('href'))
    for (const href of [
      '/account-pool',
      '/intelligence',
      '/help/api',
      '/about',
    ]) {
      expect(urls).not.toContain(href)
    }
  })

  test('the desktop service dropdown opens by keyboard and navigates through its three destinations', async () => {
    const router = await renderHeader(false, '/help', true)
    const user = userEvent.setup()
    screen.getByRole('button', { name: 'Service Center' }).focus()
    await user.keyboard('{Enter}')
    const menu = await screen.findByRole('menu', { name: 'Service Center' })
    expect(
      within(menu)
        .getAllByRole('menuitem')
        .map((item) => item.textContent)
    ).toEqual(['Invitation Rewards', 'System announcements', 'Contact us'])
    await user.click(
      within(menu).getByRole('menuitem', { name: 'System announcements' })
    )
    await waitFor(() =>
      expect(router.state.location.pathname).toBe('/system-announcements')
    )
    await waitFor(() =>
      expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    )
  })

  test('the compact menu includes service destinations and closes after choosing one', async () => {
    const router = await renderHeader(false, '/pricing', true)
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: 'Toggle navigation menu' })
    )
    const menu = await screen.findByRole('dialog', {
      name: 'Toggle navigation menu',
    })
    expect(within(menu).getByText('Service Center')).toBeVisible()
    for (const name of [
      'Invitation Rewards',
      'System announcements',
      'Contact us',
    ]) {
      expect(within(menu).getByRole('link', { name })).toBeVisible()
    }
    await user.click(within(menu).getByRole('link', { name: 'Contact us' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/contact'))
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
  })

  test('scrolling contracts the pill without retaining a viewport-sized desktop navigation', async () => {
    await renderHeader()
    const banner = screen.getByRole('banner')
    const desktopLinks = within(banner).getByRole('link', {
      name: 'Home',
    }).parentElement
    expect(banner.firstElementChild).toHaveClass('@container/public-header')
    expect(desktopLinks).toHaveClass('@min-[64rem]/public-header:flex')
    expect(desktopLinks).not.toHaveClass('lg:flex')
    expect(
      within(banner).getByRole('button', { name: 'Toggle navigation menu' })
        .parentElement
    ).toHaveClass('@min-[64rem]/public-header:hidden')

    vi.spyOn(window, 'scrollY', 'get').mockReturnValue(100)
    act(() => window.dispatchEvent(new Event('scroll')))
    expect(banner.firstElementChild).toHaveClass('max-w-[52rem]')
    expect(desktopLinks).toHaveClass('@min-[64rem]/public-header:flex')
  })

  test('the compact entry opens by keyboard, traps focus and restores it on Escape', async () => {
    const user = userEvent.setup()
    await renderHeader()
    const trigger = screen.getByRole('button', {
      name: 'Toggle navigation menu',
    })
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    trigger.focus()
    await user.keyboard('{Enter}')
    const menu = await screen.findByRole('dialog', {
      name: 'Toggle navigation menu',
    })
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    expect(within(menu).getByRole('link', { name: 'Home' })).toBeVisible()
    expect(within(menu).getByRole('link', { name: 'Sign in' })).toBeVisible()
    await waitFor(() =>
      expect(menu.contains(document.activeElement)).toBe(true)
    )
    within(menu).getByRole('link', { name: 'Sign in' }).focus()
    await user.tab()
    expect(menu.contains(document.activeElement)).toBe(true)
    within(menu).getByRole('button', { name: 'Close' }).focus()
    await user.tab({ shift: true })
    expect(menu.contains(document.activeElement)).toBe(true)
    await user.keyboard('{Escape}')
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(document.activeElement).toBe(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
  })

  test('navigating from the compact menu closes it and preserves SPA navigation', async () => {
    const user = userEvent.setup()
    const router = await renderHeader()
    await user.click(
      screen.getByRole('button', { name: 'Toggle navigation menu' })
    )
    const menu = await screen.findByRole('dialog', {
      name: 'Toggle navigation menu',
    })
    await user.click(within(menu).getByRole('link', { name: 'Docs' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/help'))
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
  })

  test('a newer route change dismisses an open compact menu', async () => {
    const user = userEvent.setup()
    const router = await renderHeader()
    await user.click(
      screen.getByRole('button', { name: 'Toggle navigation menu' })
    )
    await screen.findByRole('dialog', { name: 'Toggle navigation menu' })
    await act(() => router.navigate({ to: '/help' }))
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
  })

  test('long translated labels remain complete and can wrap inside a scrollable compact menu', async () => {
    const longLabel =
      'DocumentationPourLesAdministrateursEtLesDéveloppeursSansTroncature'
    i18next.addResourceBundle('fr', 'translation', { Docs: longLabel })
    await i18next.changeLanguage('fr')
    const user = userEvent.setup()
    await renderHeader()
    await user.click(
      screen.getByRole('button', { name: 'Toggle navigation menu' })
    )
    const menu = await screen.findByRole('dialog', {
      name: 'Toggle navigation menu',
    })
    const docs = within(menu).getByRole('link', { name: longLabel })
    expect(docs).toHaveClass('wrap-anywhere')
    expect(docs).not.toHaveClass('truncate', 'whitespace-nowrap')
    expect(docs.closest('nav')?.parentElement).toHaveClass(
      'min-h-0',
      'overflow-y-auto'
    )
  })

  test.each(['/', '/pricing'])(
    'a protected compact link on %s closes navigation and preserves the cancellable sign-in prompt',
    async (path) => {
      const user = userEvent.setup()
      await renderHeader(true, path)
      await user.click(
        screen.getByRole('button', { name: 'Toggle navigation menu' })
      )
      const menu = await screen.findByRole('dialog', {
        name: 'Toggle navigation menu',
      })
      await user.click(within(menu).getByRole('link', { name: 'Model Square' }))
      const prompt = await screen.findByRole('dialog', {
        name: 'Sign in required',
      })
      expect(
        screen.queryByRole('dialog', { name: 'Toggle navigation menu' })
      ).not.toBeInTheDocument()
      await user.click(within(prompt).getByRole('button', { name: 'Cancel' }))
      await waitFor(() =>
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
      )
    }
  )

  test('growing the header back to desktop width dismisses an open compact menu', async () => {
    const user = userEvent.setup()
    await renderHeader()
    await user.click(
      screen.getByRole('button', { name: 'Toggle navigation menu' })
    )
    await screen.findByRole('dialog', { name: 'Toggle navigation menu' })
    expect(observedHeader).toBe(
      screen.getByRole('banner', { hidden: true }).firstElementChild
    )
    // Narrow/mobile, tablet and the 52rem floating pill must not dismiss it.
    for (const width of [480, 752, 808]) {
      act(() =>
        notifyResize?.(
          [
            {
              target: observedHeader,
              contentRect: { width },
            } as ResizeObserverEntry,
          ],
          {} as ResizeObserver
        )
      )
      expect(
        screen.getByRole('dialog', { name: 'Toggle navigation menu' })
      ).toBeInTheDocument()
    }
    act(() =>
      notifyResize?.(
        [
          {
            target: observedHeader,
            contentRect: { width: 1100 },
          } as ResizeObserverEntry,
        ],
        {} as ResizeObserver
      )
    )
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
  })
})

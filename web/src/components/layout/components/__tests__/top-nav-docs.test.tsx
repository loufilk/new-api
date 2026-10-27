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
import { afterEach, describe, expect, test } from 'vitest'

import '@/extensions/bootstrap'
import { useTopNavLinks } from '@/hooks/use-top-nav-links'
import { STATUS_QUERY_KEY } from '@/lib/status-query'

import { TopNav } from '../top-nav'

/**
 * The Docs entry must stay inside the New API SPA for every internal or legacy
 * value, so a click changes the router location instead of loading the
 * marketing documentation as a document.
 */

const queryClients: QueryClient[] = []

function NavHarness() {
  const links = useTopNavLinks()
  return <TopNav links={links} />
}

async function renderTopNav(status: Record<string, unknown>) {
  const rootRoute = createRootRoute({ component: NavHarness })
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: () => <div>home</div>,
  })
  const helpRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/help',
    component: () => <div>help</div>,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, helpRoute]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  queryClients.push(queryClient)
  queryClient.setQueryData(STATUS_QUERY_KEY, status)

  await router.load()
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return router
}

afterEach(() => {
  queryClients.splice(0).forEach((client) => client.clear())
})

describe('top navigation docs entry rendering', () => {
  test('clicking the legacy quickstart entry navigates the SPA to /help', async () => {
    const user = userEvent.setup()
    const router = await renderTopNav({
      docs_link: 'https://chuanapi.com/docs/quickstart/',
    })

    const nav = screen.getByRole('navigation')
    const docs = within(nav).getByRole('link', { name: 'Docs' })
    expect(docs).toHaveAttribute('href', '/help')
    expect(docs).not.toHaveAttribute('target')

    await user.click(docs)

    await waitFor(() => expect(router.state.location.pathname).toBe('/help'))
  })

  test('an explicit external docs URL renders a same-tab document link', async () => {
    await renderTopNav({ docs_link: 'https://docs.example.com/guide' })

    const nav = screen.getByRole('navigation')
    const docs = within(nav).getByRole('link', { name: 'Docs' })
    expect(docs).toHaveAttribute('href', 'https://docs.example.com/guide')
    expect(docs).not.toHaveAttribute('target')
    expect(docs).not.toHaveAttribute('rel')
  })
})

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
import { render } from '@testing-library/react'
import type { ReactNode } from 'react'
import { vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

export const ok = (data: unknown) => ({ data: { success: true, data } })
export const page = (items: unknown[]) => ({
  items,
  total: items.length,
  page: 1,
  page_size: 20,
})
export function setupExpansionUser(role = 10) {
  sessionStorage.clear()
  useAuthStore.getState().auth.setUser({ id: 42, username: 'operator', role })
  vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined)
  vi.spyOn(api, 'get').mockResolvedValue(ok(page([])))
  vi.spyOn(api, 'request').mockImplementation(async (config) => {
    const body = config.data as { action: string; request_id: string }
    if (!config.url?.endsWith('/prepare')) return ok({ id: 10 })
    return ok({
      verification_context: {
        action: body.action,
        request_id: body.request_id,
        agent_user_id: 8,
        profile_version: 3,
        policy_version: 7,
        currency: 'USD',
        amount_minor: '100',
        quota: 0,
        target_type: 'business_resource',
        target_id: '8',
        target_version: 2,
        recipient_version: 0,
        recipient_digest: '',
        parameters_hash: 'server-bound-parameters',
      },
      summary: {
        action: body.action,
        agent_user_id: 8,
        policy_version: 7,
        currency: 'USD',
        currency_exponent: 2,
        amount_minor: '100',
        fee_minor: '0',
        net_minor: '100',
        quota: 0,
        target_label: 'Reviewed source 8',
        recipient_label: '',
      },
      status_url: `/api/agent-platform/operations/${body.request_id}`,
    })
  })
}
export async function mountExpansion(node: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const root = createRootRoute()
  const route = createRoute({
    getParentRoute: () => root,
    path: '/test',
    component: () => node,
  })
  const other = createRoute({
    getParentRoute: () => root,
    path: '/other',
    component: () => <p>Other page</p>,
  })
  const router = createRouter({
    routeTree: root.addChildren([route, other]),
    history: createMemoryHistory({ initialEntries: ['/test'] }),
  })
  await router.load()
  const view = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return { ...view, client, router }
}

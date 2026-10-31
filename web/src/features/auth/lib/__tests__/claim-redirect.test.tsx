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
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import axios from 'axios'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

const secret = 'private-delivery-bearer'
const originalAdapter = axios.defaults.adapter

beforeEach(() => {
  vi.resetModules()
  localStorage.clear()
  sessionStorage.clear()
})

afterEach(() => {
  axios.defaults.adapter = originalAdapter
  localStorage.clear()
  sessionStorage.clear()
})

async function loadProtectedRoute(entry: string, expired = false) {
  const requests: string[] = []
  axios.defaults.adapter = async (config) => {
    requests.push(config.url ?? '')
    return {
      data: { success: false },
      status: 401,
      statusText: 'Unauthorized',
      headers: {},
      config,
    }
  }
  const { useAuthStore } = await import('@/stores/auth-store')
  useAuthStore.getState().auth.reset('idle')
  if (expired) {
    useAuthStore.getState().auth.setBundle({
      access_token: 'expired-access',
      token_type: 'Bearer',
      access_expires_at: 1,
      user: { id: 42, username: 'customer', role: 1 },
      session: {
        sid: 'expired-session',
        current: true,
        login_method: 'password',
        ip: '127.0.0.1',
        user_agent: 'test',
        created_at: 1,
        last_active_at: 1,
        expires_at: 2,
      },
    })
  }
  const { Route } = await import('@/routes/_authenticated/route')
  const beforeLoad = Route.options.beforeLoad as unknown as (args: {
    location: { href: string }
  }) => Promise<unknown>
  const root = createRootRoute()
  const protectedRoute = createRoute({
    getParentRoute: () => root,
    id: 'authenticated',
    beforeLoad: (args) => beforeLoad(args),
  })
  const claim = createRoute({
    getParentRoute: () => protectedRoute,
    path: '/agent-platform/claim',
  })
  const wallet = createRoute({
    getParentRoute: () => protectedRoute,
    path: '/wallet',
  })
  const signIn = createRoute({ getParentRoute: () => root, path: '/sign-in' })
  const history = createMemoryHistory({ initialEntries: [entry] })
  const router = createRouter({
    routeTree: root.addChildren([
      protectedRoute.addChildren([claim, wallet]),
      signIn,
    ]),
    history,
  })
  await router.load()
  return { router, requests }
}

test.each([
  { label: 'anonymous', expired: false },
  { label: 'expired', expired: true },
])(
  'an $label claim session redirects without copying the bearer to search or storage',
  async ({ expired }) => {
    const { router, requests } = await loadProtectedRoute(
      `/agent-platform/claim#code=${secret}`,
      expired
    )
    expect(router.state.location.pathname).toBe('/sign-in')
    expect(router.state.location.search).toEqual({
      redirect: '/agent-platform/claim',
    })
    expect(router.state.location.href).not.toContain(secret)
    expect(router.history.length).toBe(1)
    expect(requests).toEqual(['/api/user/auth/refresh'])
    expect(JSON.stringify(localStorage)).not.toContain(secret)
    expect(JSON.stringify(sessionStorage)).not.toContain(secret)
  }
)

test('ordinary protected destinations retain their non-secret search and fragment', async () => {
  const { router } = await loadProtectedRoute('/wallet?tab=history#recent')
  expect(router.state.location.search).toEqual({
    redirect: '/wallet?tab=history#recent',
  })
})

test('OAuth persistence strips a delivery bearer even when given an unsanitized redirect directly', async () => {
  const { rememberOAuthLoginRedirect, consumeOAuthLoginRedirect } =
    await import('../oauth-callback-mode')
  rememberOAuthLoginRedirect(
    'claim-login',
    `/agent-platform/claim#code=${secret}`
  )
  expect(JSON.stringify(sessionStorage)).not.toContain(secret)
  expect(consumeOAuthLoginRedirect('claim-login')).toBe('/agent-platform/claim')
})

test('redirect normalization drops claim query and fragment but preserves the safe destination', async () => {
  const { sanitizeAuthRedirect } = await import('../auth-redirect')
  expect(
    sanitizeAuthRedirect(
      `/agent-platform/claim/?code=${secret}#code=${secret}`,
      window.location.origin
    )
  ).toBe('/agent-platform/claim')
})

test('OAuth callback consumption discards a bearer from a previously saved redirect', async () => {
  const { consumeOAuthLoginRedirect } = await import('../oauth-callback-mode')
  sessionStorage.setItem(
    'oauth_login_redirect:old-login',
    `/agent-platform/claim#code=${secret}`
  )
  expect(consumeOAuthLoginRedirect('old-login')).toBe('/agent-platform/claim')
  expect(sessionStorage.getItem('oauth_login_redirect:old-login')).toBeNull()
})

test.each([
  '/AGENT-PLATFORM/CLAIM',
  '/%61gent-platform/cl%61im',
  '/agent-platform/claim/',
])(
  'the real guard removes a bearer from the matched claim alias %s',
  async (path) => {
    const { router } = await loadProtectedRoute(`${path}#code=${secret}`)
    expect(router.state.location.pathname).toBe('/sign-in')
    expect(router.state.location.search).toEqual({
      redirect: '/agent-platform/claim',
    })
    expect(router.state.location.href).not.toContain(secret)
  }
)

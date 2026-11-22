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
  RouterProvider,
} from '@tanstack/react-router'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import axios, { AxiosError } from 'axios'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const entry = '/wallet?tab=history#recent'
const adapter = axios.defaults.adapter
const fixture = {
  access_token: 'fixture-only',
  token_type: 'Bearer',
  access_expires_at: 2_000_000_000,
  user: { id: 42, username: 'fixture', role: 1 },
  session: {
    sid: 'fixture-session',
    current: true,
    login_method: 'password',
    ip: '127.0.0.1',
    user_agent: 'fixture',
    created_at: 1,
    last_active_at: 1,
    expires_at: 2_000_000_000,
  },
}

beforeEach(() => {
  vi.resetModules()
  sessionStorage.clear()
  document.cookie = 'new_api_has_session=1; Path=/'
})

afterEach(() => {
  axios.defaults.adapter = adapter
  sessionStorage.clear()
  document.cookie = 'new_api_has_session=; Path=/; Max-Age=0'
})

async function loadWallet() {
  const { bootstrapAuthentication } = await import('@/lib/auth-session')
  const { Route } = await import('@/routes/_authenticated/route')
  const guard = Route.options.beforeLoad as unknown as (args: {
    location: { href: string }
  }) => Promise<unknown>
  const root = createRootRoute({
    beforeLoad: async () => {
      await bootstrapAuthentication()
    },
  })
  const protectedRoute = createRoute({
    id: 'auth',
    getParentRoute: () => root,
    beforeLoad: (args) => guard(args),
    errorComponent: Route.options.errorComponent,
  })
  const wallet = createRoute({
    getParentRoute: () => protectedRoute,
    path: '/wallet',
    component: () => <p>Private wallet</p>,
  })
  const signIn = createRoute({ getParentRoute: () => root, path: '/sign-in' })
  const router = createRouter({
    routeTree: root.addChildren([protectedRoute.addChildren([wallet]), signIn]),
    history: createMemoryHistory({ initialEntries: [entry] }),
  })
  await router.load()
  return router
}

it('keeps a cold protected deep link through a 429 and hard reload, then unlocks it after a verified retry', async () => {
  let now = 1_900_000_000_000
  vi.spyOn(Date, 'now').mockImplementation(() => now)
  let requests = 0
  axios.defaults.adapter = async (config) => {
    requests++
    if (requests === 1) {
      throw new AxiosError('Too many requests', undefined, config, undefined, {
        status: 429,
        statusText: 'Too Many Requests',
        headers: { 'retry-after': '45' },
        data: {},
        config,
      })
    }
    return {
      status: 200,
      statusText: 'OK',
      data: { success: true, data: fixture },
      headers: {},
      config,
    }
  }
  expect((await loadWallet()).state.location.href).toBe(entry)
  expect(requests).toBe(1)
  vi.resetModules() // an empty in-memory store, but the same tab and cookie
  const router = await loadWallet()
  render(<RouterProvider router={router} />)
  expect(await screen.findByRole('button', { name: /Retry/i })).toBeDisabled()
  expect(screen.queryByText('Private wallet')).not.toBeInTheDocument()
  expect(document.cookie).toContain('new_api_has_session=1')
  now += 44_000
  await act(() => router.invalidate())
  expect(requests).toBe(1)
  now += 1000
  await waitFor(() =>
    expect(screen.getByRole('button', { name: /Retry/i })).toBeEnabled()
  )
  fireEvent.click(screen.getByRole('button', { name: /Retry/i }))
  expect(await screen.findByText('Private wallet')).toBeVisible()
  expect(router.state.location.href).toBe(entry)
  expect(requests).toBe(2)
})

it.each([0, 503])(
  'does not authorize a stale access token or clear it when refresh fails with %s',
  async (status) => {
    let requests = 0
    axios.defaults.adapter = async (config) => {
      requests++
      throw new AxiosError(
        'Unavailable',
        undefined,
        config,
        undefined,
        status
          ? {
              status,
              statusText: 'Unavailable',
              headers: {},
              data: {},
              config,
            }
          : undefined
      )
    }
    const { useAuthStore } = await import('@/stores/auth-store')
    useAuthStore.getState().auth.setBundle({ ...fixture, access_expires_at: 1 })
    const router = await loadWallet()
    expect(router.state.location.href).toBe(entry)
    render(<RouterProvider router={router} />)
    expect(await screen.findByRole('button', { name: /Retry/i })).toBeDisabled()
    expect(screen.queryByText('Private wallet')).not.toBeInTheDocument()
    expect(useAuthStore.getState().auth.user?.id).toBe(fixture.user.id)
    expect(useAuthStore.getState().auth.accessToken).toBe('fixture-only')
    expect(requests).toBe(1)
  }
)

it('keeps a hintless visitor on the requested URL until the server explicitly confirms 401', async () => {
  document.cookie = 'new_api_has_session=; Path=/; Max-Age=0'
  let now = 1_900_000_000_000
  vi.spyOn(Date, 'now').mockImplementation(() => now)
  let requests = 0
  axios.defaults.adapter = async (config) => {
    const limited = ++requests === 1
    throw new AxiosError('Refresh rejected', undefined, config, undefined, {
      status: limited ? 429 : 401,
      statusText: 'Rejected',
      headers: limited
        ? { 'retry-after': new Date(now + 5000).toUTCString() }
        : {},
      data: {},
      config,
    })
  }
  const router = await loadWallet()
  expect(router.state.location.href).toBe(entry)
  await router.invalidate({ sync: true })
  expect(requests).toBe(1)
  now += 5000
  await router.invalidate({ sync: true })
  await waitFor(() => expect(router.state.location.pathname).toBe('/sign-in'))
  expect(router.state.location.search).toEqual({ redirect: entry })
  expect(requests).toBe(2)
})

it('allows an intentional sign-in when a stale in-memory user cannot be verified', async () => {
  axios.defaults.adapter = async (config) => {
    throw new AxiosError('Offline', undefined, config)
  }
  const { useAuthStore } = await import('@/stores/auth-store')
  useAuthStore.getState().auth.setBundle({ ...fixture, access_expires_at: 1 })
  const { Route } = await import('@/routes/(auth)/sign-in')
  const guard = Route.options.beforeLoad as unknown as (args: {
    search: { redirect: string }
  }) => Promise<unknown>
  await expect(guard({ search: { redirect: entry } })).resolves.toBeUndefined()
})

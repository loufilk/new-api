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
import { act, render, screen, waitFor } from '@testing-library/react'
import { StrictMode } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { Route as CallbackRoute } from '@/routes/oauth/$provider'
import { useAuthStore } from '@/stores/auth-store'

const clients: QueryClient[] = []

function renderCallback(entry: string) {
  const root = createRootRoute({ component: Outlet })
  const callback = createRoute({
    getParentRoute: () => root,
    path: '/oauth/$provider',
    component: CallbackRoute.options.component,
  })
  const otp = createRoute({
    getParentRoute: () => root,
    path: '/otp',
    component: () => <div>Second factor required</div>,
  })
  const signIn = createRoute({
    getParentRoute: () => root,
    path: '/sign-in',
    component: () => <div>Sign-in page</div>,
  })
  const router = createRouter({
    routeTree: root.addChildren([callback, otp, signIn]),
    history: createMemoryHistory({ initialEntries: [entry] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <StrictMode>
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </StrictMode>
  )
  return router
}

beforeEach(() => {
  useAuthStore.getState().auth.reset('complete')
  window.sessionStorage.clear()
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset('idle')
  vi.restoreAllMocks()
})

it('sends a cancelled Google callback to the backend once and returns to sign-in without authentication', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data:
      url === '/api/status'
        ? { success: true, data: {} }
        : {
            success: false,
            message: 'Google sign-in was cancelled. Please try again.',
          },
  }))
  const router = renderCallback(
    '/oauth/google?state=google-state&error=access_denied'
  )
  expect(await screen.findByText('Sign-in page')).toBeVisible()
  expect(
    get.mock.calls.filter(([url]) => url === '/api/oauth/google')
  ).toHaveLength(1)
  expect(get).toHaveBeenCalledWith(
    '/api/oauth/google',
    expect.objectContaining({
      params: expect.objectContaining({
        state: 'google-state',
        error: 'access_denied',
      }),
      skipAuthRefresh: true,
    })
  )
  expect(useAuthStore.getState().auth.user).toBeNull()
  expect(router.history.length).toBe(1)
})

it('deduplicates a pending Google callback and passes its challenge into the existing second-factor flow', async () => {
  const challenge = {
    require_verification: true,
    flow_token: 'google-login-challenge',
    expires_at: Math.floor(Date.now() / 1000) + 300,
    methods: [{ method: '2fa', available: true }],
  }
  let finish!: (value: {
    data: { success: boolean; data: typeof challenge }
  }) => void
  const response = new Promise<{
    data: { success: boolean; data: typeof challenge }
  }>((resolve) => {
    finish = resolve
  })
  const get = vi
    .spyOn(api, 'get')
    .mockImplementation((url) =>
      url === '/api/status'
        ? Promise.resolve({ data: { success: true, data: {} } })
        : response
    )
  const router = renderCallback(
    '/oauth/google?state=google-state&code=synthetic-code&redirect=https%3A%2F%2Funtrusted.example'
  )
  await waitFor(() =>
    expect(
      get.mock.calls.filter(([url]) => url === '/api/oauth/google')
    ).toHaveLength(1)
  )
  expect(useAuthStore.getState().auth.user).toBeNull()
  await act(async () => {
    finish({ data: { success: true, data: challenge } })
    await response
  })
  expect(await screen.findByText('Second factor required')).toBeVisible()
  expect(useAuthStore.getState().auth.user).toBeNull()
  expect(useAuthStore.getState().auth.pendingLoginVerification).toEqual({
    challenge,
    redirectTo: undefined,
  })
  expect(router.state.location.pathname).toBe('/otp')
  expect(router.history.length).toBe(1)
  expect(
    get.mock.calls.filter(([url]) => url === '/api/oauth/google')
  ).toHaveLength(1)
})

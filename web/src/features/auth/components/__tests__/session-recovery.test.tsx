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
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import axios, { AxiosError, AxiosHeaders } from 'axios'
import { useLayoutEffect } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import {
  requireAuthenticatedSession,
  redirectAuthenticatedSession,
} from '@/features/auth/lib/session-route-guards'
import {
  AuthSessionUnavailableError,
  bootstrapAuthentication,
  clearAuthentication,
} from '@/lib/auth-session'
import { hasSessionHint, markSessionHint } from '@/lib/session-hint'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import { AuthSessionError } from '../auth-session-error'

let now = Date.now()
const response = {
  status: 200,
  statusText: 'OK',
  headers: new AxiosHeaders(),
  config: { headers: new AxiosHeaders() },
}

function refreshError(status: number) {
  return new AxiosError(
    'Refresh rejected',
    'ERR_BAD_RESPONSE',
    undefined,
    undefined,
    {
      ...response,
      status,
      headers: new AxiosHeaders({ 'Retry-After': '2' }),
      data: {},
    }
  )
}

function recoveryRouter() {
  const root = createRootRoute({
    beforeLoad: async () => {
      await bootstrapAuthentication()
    },
    component: Outlet,
  })
  const protectedRoute = createRoute({
    getParentRoute: () => root,
    id: 'authenticated',
    beforeLoad: requireAuthenticatedSession,
    component: Outlet,
    errorComponent: AuthSessionError,
  })
  const withdrawals = createRoute({
    getParentRoute: () => protectedRoute,
    path: '/referral-withdrawals',
    component: () => <h1>Withdrawal requests</h1>,
  })
  const signIn = createRoute({
    getParentRoute: () => root,
    path: '/sign-in',
    beforeLoad: redirectAuthenticatedSession,
    component: () => <h1>Sign-in form</h1>,
    errorComponent: AuthSessionError,
  })
  return createRouter({
    routeTree: root.addChildren([
      protectedRoute.addChildren([withdrawals]),
      signIn,
    ]),
    history: createMemoryHistory({ initialEntries: ['/referral-withdrawals'] }),
    defaultPendingMs: 0,
    defaultPendingMinMs: 0,
  })
}

beforeEach(() => {
  now = Date.now()
  vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined)
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
  vi.spyOn(Date, 'now').mockImplementation(() => now)
  clearAuthentication(false, 'idle')
  markSessionHint(Math.floor(now / 1000) + 3600)
})

afterEach(() => {
  clearAuthentication(false, 'idle')
  vi.useRealTimers()
  vi.restoreAllMocks()
})

test('rate-limited navigation keeps the original destination, disables early retries and returns there after successful recovery', async () => {
  const request = vi
    .spyOn(axios.Axios.prototype, 'request')
    .mockRejectedValue(refreshError(429))
  const router = recoveryRouter()
  const user = userEvent.setup()
  render(<RouterProvider router={router} />)

  expect(
    await screen.findByText('Session verification is temporarily unavailable')
  ).toBeVisible()
  expect(screen.queryByText('Withdrawal requests')).not.toBeInTheDocument()
  expect(screen.queryByText('Sign-in form')).not.toBeInTheDocument()
  expect(router.state.location.pathname).toBe('/referral-withdrawals')
  const earlyRetry = screen.getByRole('button', { name: 'Retry in 2s' })
  expect(earlyRetry).toBeDisabled()
  fireEvent.click(earlyRetry)
  expect(request).toHaveBeenCalledTimes(1)

  await act(async () => {
    now += 2000
    vi.advanceTimersByTime(2000)
  })
  const bundle: AuthBundle = {
    access_token: 'recovered-access',
    token_type: 'Bearer',
    access_expires_at: Math.floor(now / 1000) + 600,
    user: { id: 42, username: 'recovered-user', role: 1 },
    session: {
      sid: 'recovered-session',
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'test',
      created_at: 100,
      last_active_at: 100,
      expires_at: Math.floor(now / 1000) + 3600,
    },
  }
  request.mockResolvedValue({
    ...response,
    data: { success: true, data: bundle },
  })
  expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: 'Retry' }))

  expect(await screen.findByText('Withdrawal requests')).toBeVisible()
  expect(router.state.location.pathname).toBe('/referral-withdrawals')
  expect(screen.queryByText('Sign-in form')).not.toBeInTheDocument()
  expect(request).toHaveBeenCalledTimes(2)
})

test('a retry that confirms a 401 leaves the recovery state and opens sign-in with the original destination', async () => {
  const request = vi
    .spyOn(axios.Axios.prototype, 'request')
    .mockRejectedValue(refreshError(429))
  const router = recoveryRouter()
  const user = userEvent.setup()
  render(<RouterProvider router={router} />)
  await screen.findByText('Session verification is temporarily unavailable')

  await act(async () => {
    now += 2000
    vi.advanceTimersByTime(2000)
  })
  request.mockRejectedValue(refreshError(401))
  await user.click(screen.getByRole('button', { name: 'Retry' }))

  expect(await screen.findByText('Sign-in form')).toBeVisible()
  expect(router.state.location.pathname).toBe('/sign-in')
  expect(router.state.location.search).toMatchObject({
    redirect: '/referral-withdrawals',
  })
  expect(useAuthStore.getState().auth).toMatchObject({
    user: null,
    accessToken: null,
    session: null,
  })
  expect(hasSessionHint()).toBe(false)
  expect(request).toHaveBeenCalledTimes(2)
})

test('a cooldown that ends between render and commit still enables the retry button', async () => {
  const deadline = now + 1
  const error = new AuthSessionUnavailableError({
    kind: 'transient_error',
    error: null,
    retryAt: deadline,
  })
  function CommitAtDeadline() {
    useLayoutEffect(() => {
      now = deadline
    }, [])
    return <AuthSessionError error={error} reset={() => undefined} />
  }
  const root = createRootRoute({ component: CommitAtDeadline })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(<RouterProvider router={router} />)
  expect(
    await screen.findByRole('button', { name: 'Retry in 1s' })
  ).toBeDisabled()

  await act(async () => {
    vi.advanceTimersByTime(1000)
  })

  expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
})

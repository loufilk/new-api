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
import { isRedirect } from '@tanstack/react-router'
import axios, { AxiosError, AxiosHeaders } from 'axios'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import {
  redirectAuthenticatedSession,
  requireAuthenticatedSession,
} from '@/features/auth/lib/session-route-guards'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import {
  applyAuthBundle,
  bootstrapAuthentication,
  clearAuthentication,
  refreshAuthentication,
  resolveAuthentication,
} from '../auth-session'
import {
  clearSessionHint,
  hasSessionHint,
  markSessionHint,
} from '../session-hint'

const initialTime = Math.floor(Date.now() / 1000) * 1000
let now = initialTime
const response = {
  status: 200,
  statusText: 'OK',
  headers: new AxiosHeaders(),
  config: { headers: new AxiosHeaders() },
}

function authenticatedBundle(id = 42): AuthBundle {
  return {
    access_token: `test-access-${id}`,
    token_type: 'Bearer',
    access_expires_at: Math.floor(now / 1000) + 600,
    user: { id, username: `user-${id}`, role: 1 },
    session: {
      sid: `test-session-${id}`,
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'test',
      created_at: 100,
      last_active_at: 100,
      expires_at: Math.floor(now / 1000) + 3600,
    },
  }
}

function rejectedRefresh(status = 429, retryAfter?: string) {
  return new AxiosError(
    'Refresh unavailable',
    'ERR_BAD_RESPONSE',
    undefined,
    undefined,
    {
      ...response,
      status,
      headers: new AxiosHeaders(
        retryAfter === undefined ? {} : { 'Retry-After': retryAfter }
      ),
      data: {},
    }
  )
}

const protectedNavigation = () =>
  requireAuthenticatedSession({ location: { href: '/referral-withdrawals' } })

beforeEach(() => {
  now = initialTime
  vi.spyOn(Date, 'now').mockImplementation(() => now)
  clearAuthentication(false, 'idle')
  markSessionHint(Math.floor(now / 1000) + 3600)
})

afterEach(() => {
  clearAuthentication(false, 'idle')
  clearSessionHint()
  vi.restoreAllMocks()
})

test('a cold protected navigation shares a rate-limited bootstrap and shows a retryable error without redirecting to sign-in', async () => {
  const request = vi
    .spyOn(axios.Axios.prototype, 'request')
    .mockRejectedValue(rejectedRefresh(429, '30'))

  expect((await bootstrapAuthentication()).kind).toBe('transient_error')
  await expect(protectedNavigation()).rejects.toMatchObject({
    name: 'AuthSessionUnavailableError',
  })
  expect(request).toHaveBeenCalledTimes(1)
  expect(hasSessionHint()).toBe(true)
  expect(useAuthStore.getState().auth.user).toBeNull()
})

test('an expired access token does not allow a protected page through a temporary refresh failure', async () => {
  const bundle = authenticatedBundle()
  bundle.access_expires_at = Math.floor(now / 1000) - 1
  applyAuthBundle(bundle, false)
  vi.spyOn(axios.Axios.prototype, 'request').mockRejectedValue(
    rejectedRefresh(429, '2')
  )

  await expect(protectedNavigation()).rejects.toMatchObject({
    name: 'AuthSessionUnavailableError',
  })
  expect(useAuthStore.getState().auth.session?.sid).toBe(bundle.session.sid)
  expect(hasSessionHint()).toBe(true)
})

test('the sign-in guard also shows a temporary recovery failure instead of asking for credentials', async () => {
  vi.spyOn(axios.Axios.prototype, 'request').mockRejectedValue(
    rejectedRefresh(429, '2')
  )

  await expect(
    redirectAuthenticatedSession({ search: { redirect: '/affiliate' } })
  ).rejects.toMatchObject({ name: 'AuthSessionUnavailableError' })
})

test.each([
  { name: 'delta seconds', header: '2', milliseconds: 2000 },
  {
    name: 'HTTP date',
    header: new Date(initialTime + 2000).toUTCString(),
    milliseconds: 2000,
  },
  { name: 'invalid negative value', header: '-1', milliseconds: 30_000 },
  { name: 'invalid infinite value', header: 'Infinity', milliseconds: 30_000 },
  { name: 'missing header', header: undefined, milliseconds: 30_000 },
])(
  'refresh callers respect $name cooldown and recover only after its deadline',
  async ({ header, milliseconds }) => {
    const request = vi
      .spyOn(axios.Axios.prototype, 'request')
      .mockRejectedValue(rejectedRefresh(429, header))
    const first = await refreshAuthentication()
    expect(first.kind).toBe('transient_error')

    now += milliseconds - 1
    expect((await resolveAuthentication()).kind).toBe('transient_error')
    expect(request).toHaveBeenCalledTimes(1)

    now += 1
    const bundle = authenticatedBundle()
    request.mockResolvedValue({
      ...response,
      data: { success: true, data: bundle },
    })
    expect(await resolveAuthentication()).toEqual({
      kind: 'authenticated',
      bundle,
    })
    expect(request).toHaveBeenCalledTimes(2)
    await expect(protectedNavigation()).resolves.toBeUndefined()
    expect(request).toHaveBeenCalledTimes(2)
  }
)

test('raw response headers are read case-insensitively for the refresh cooldown', async () => {
  const failure = rejectedRefresh(429)
  if (!failure.response) throw new Error('Missing test response')
  failure.response.headers = { 'rEtRy-AfTeR': '2' }
  const request = vi
    .spyOn(axios.Axios.prototype, 'request')
    .mockRejectedValue(failure)

  expect(await refreshAuthentication()).toMatchObject({
    kind: 'transient_error',
    retryAt: now + 2000,
  })
  now += 1999
  expect((await refreshAuthentication()).kind).toBe('transient_error')
  expect(request).toHaveBeenCalledTimes(1)
})

test('temporary server failures use a short shared retry delay without marking the session anonymous', async () => {
  const request = vi
    .spyOn(axios.Axios.prototype, 'request')
    .mockRejectedValue(rejectedRefresh(503))
  expect((await refreshAuthentication()).kind).toBe('transient_error')
  expect((await resolveAuthentication()).kind).toBe('transient_error')
  expect(request).toHaveBeenCalledTimes(1)
  expect(hasSessionHint()).toBe(true)

  now += 5000
  const bundle = authenticatedBundle()
  request.mockResolvedValue({
    ...response,
    data: { success: true, data: bundle },
  })
  expect((await resolveAuthentication()).kind).toBe('authenticated')
})

test('a confirmed 401 clears credentials and the hint, and still redirects to sign-in', async () => {
  const bundle = authenticatedBundle()
  bundle.access_expires_at = Math.floor(now / 1000) - 1
  applyAuthBundle(bundle, false)
  const request = vi
    .spyOn(axios.Axios.prototype, 'request')
    .mockRejectedValue(rejectedRefresh(401))

  const error = await protectedNavigation().catch((caught: unknown) => caught)
  expect(isRedirect(error)).toBe(true)
  expect(useAuthStore.getState().auth).toMatchObject({
    user: null,
    accessToken: null,
    session: null,
  })
  expect(hasSessionHint()).toBe(false)
  expect(await resolveAuthentication()).toEqual({ kind: 'anonymous' })
  expect(request).toHaveBeenCalledTimes(1)
})

test('a missing public hint does not prevent a protected route from recovering a valid refresh cookie', async () => {
  clearSessionHint()
  const bundle = authenticatedBundle()
  const request = vi
    .spyOn(axios.Axios.prototype, 'request')
    .mockResolvedValue({ ...response, data: { success: true, data: bundle } })

  expect(await bootstrapAuthentication()).toEqual({ kind: 'anonymous' })
  expect(request).not.toHaveBeenCalled()
  await expect(protectedNavigation()).resolves.toBeUndefined()
  expect(request).toHaveBeenCalledTimes(1)
  expect(useAuthStore.getState().auth.user?.id).toBe(bundle.user.id)
})

test('a late 429 from a previous authentication epoch cannot add a cooldown to the newly signed-in session', async () => {
  let rejectRequest!: (error: unknown) => void
  const request = vi
    .spyOn(axios.Axios.prototype, 'request')
    .mockImplementationOnce(
      () =>
        new Promise((_resolve, reject) => {
          rejectRequest = reject
        })
    )
  const pending = refreshAuthentication()
  const newer = authenticatedBundle(99)
  applyAuthBundle(newer, false)
  rejectRequest(rejectedRefresh(429, '1200'))
  await pending

  expect(useAuthStore.getState().auth.session?.sid).toBe(newer.session.sid)
  request.mockResolvedValue({
    ...response,
    data: { success: true, data: newer },
  })
  expect((await refreshAuthentication()).kind).toBe('authenticated')
  expect(request).toHaveBeenCalledTimes(2)
  expect(useAuthStore.getState().auth.user?.id).toBe(99)
})

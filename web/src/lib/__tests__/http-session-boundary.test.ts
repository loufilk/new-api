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
import { AxiosError, type InternalAxiosRequestConfig } from 'axios'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import { applyAuthBundle, clearAuthentication } from '../auth-session'
import { api } from '../http-client'

const originalAdapter = api.defaults.adapter

function makeBundle(id: number, token = `synthetic-access-${id}`): AuthBundle {
  const now = Math.floor(Date.now() / 1000)
  return {
    access_token: token,
    token_type: 'Bearer',
    access_expires_at: now + 600,
    user: { id, username: `synthetic-user-${id}`, role: 1 },
    session: {
      sid: `synthetic-session-${id}`,
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'review',
      created_at: now,
      last_active_at: now,
      expires_at: now + 3600,
    },
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

function unauthorized(config: InternalAxiosRequestConfig): AxiosError {
  return new AxiosError(
    'Expired access token',
    'ERR_BAD_REQUEST',
    config,
    undefined,
    {
      data: { code: 'AUTH_ACCESS_EXPIRED' },
      status: 401,
      statusText: 'Unauthorized',
      headers: {},
      config,
    }
  )
}

function success(
  config: InternalAxiosRequestConfig,
  data: unknown = { success: true }
) {
  return { data, status: 200, statusText: 'OK', headers: {}, config }
}

function finishRefresh(xhr: XMLHttpRequest, bundle: AuthBundle): void {
  Object.defineProperties(xhr, {
    status: { value: 200, configurable: true },
    statusText: { value: 'OK', configurable: true },
    responseText: {
      value: JSON.stringify({ success: true, data: bundle }),
      configurable: true,
    },
    readyState: { value: 4, configurable: true },
  })
  xhr.onloadend?.(new ProgressEvent('loadend'))
}

beforeEach(() => {
  applyAuthBundle(makeBundle(101), false)
  api.defaults.adapter = async () => {
    throw new Error('Unexpected API request')
  }
  vi.spyOn(XMLHttpRequest.prototype, 'send').mockImplementation(() => {
    throw new Error('Unexpected refresh request')
  })
})

afterEach(() => {
  api.defaults.adapter = originalAdapter
  clearAuthentication(false)
  vi.restoreAllMocks()
})

test('a delayed A 401 after login B cancels without replaying or refreshing B', async () => {
  const started = deferred<void>()
  const release = deferred<void>()
  const adapter = vi.fn(async (config: InternalAxiosRequestConfig) => {
    started.resolve()
    await release.promise
    throw unauthorized(config)
  })
  api.defaults.adapter = adapter
  const pending = api.post(
    '/api/token/',
    { name: 'A private key' },
    { skipErrorHandler: true }
  )
  const rejected = expect(pending).rejects.toMatchObject({
    code: 'ERR_CANCELED',
  })
  await started.promise
  clearAuthentication(false)
  applyAuthBundle(makeBundle(202), false)
  release.resolve()
  await rejected
  expect(adapter).toHaveBeenCalledTimes(1)
  expect(XMLHttpRequest.prototype.send).not.toHaveBeenCalled()
  expect(useAuthStore.getState().auth.user?.id).toBe(202)
})

test('a session change before the first interceptor microtask prevents dispatch as B', async () => {
  const adapter = vi.fn(async (config: InternalAxiosRequestConfig) =>
    success(config)
  )
  api.defaults.adapter = adapter
  const pending = api.post('/api/token/', { name: 'A private key' })
  clearAuthentication(false)
  applyAuthBundle(makeBundle(202), false)
  await expect(pending).rejects.toMatchObject({ code: 'ERR_CANCELED' })
  expect(adapter).not.toHaveBeenCalled()
})

test('an A refresh finishing after B login neither restores A nor replays the request', async () => {
  const refreshStarted = deferred<XMLHttpRequest>()
  vi.mocked(XMLHttpRequest.prototype.send).mockImplementation(function (
    this: XMLHttpRequest
  ) {
    refreshStarted.resolve(this)
  })
  const adapter = vi.fn(async (config: InternalAxiosRequestConfig) => {
    throw unauthorized(config)
  })
  api.defaults.adapter = adapter
  const pending = api.post(
    '/api/token/',
    { name: 'A private key' },
    { skipErrorHandler: true }
  )
  const rejected = expect(pending).rejects.toMatchObject({
    code: 'ERR_CANCELED',
  })
  const xhr = await refreshStarted.promise
  clearAuthentication(false)
  applyAuthBundle(makeBundle(202), false)
  finishRefresh(xhr, makeBundle(101, 'A-old-refresh'))
  await rejected
  expect(adapter).toHaveBeenCalledTimes(1)
  expect(useAuthStore.getState().auth.accessToken).toBe('synthetic-access-202')
})

test('a delayed A success is not exposed to callbacks after login B', async () => {
  const started = deferred<void>()
  const release = deferred<void>()
  api.defaults.adapter = async (config) => {
    started.resolve()
    await release.promise
    return success(config, { success: true, data: { private: 'A only' } })
  }
  const pending = api.get('/api/user/self', { disableDuplicate: true })
  const rejected = expect(pending).rejects.toMatchObject({
    code: 'ERR_CANCELED',
  })
  await started.promise
  applyAuthBundle(makeBundle(202), false)
  release.resolve()
  await rejected
})

test('logout and login with the same SID still discard a delayed auth rotation', async () => {
  const started = deferred<void>()
  const release = deferred<void>()
  api.defaults.adapter = async (config) => {
    started.resolve()
    await release.promise
    const rotation = makeBundle(101, 'old-rotation')
    return success(config, { success: true, data: rotation })
  }
  const pending = api.post('/sensitive', {}, { acceptAuthRotation: true })
  const rejected = expect(pending).rejects.toMatchObject({
    code: 'ERR_CANCELED',
  })
  await started.promise
  clearAuthentication(false)
  applyAuthBundle(makeBundle(101, 'new-login'), false)
  release.resolve()
  await rejected
  expect(useAuthStore.getState().auth.accessToken).toBe('new-login')
})

test('same-session token refresh retries once using the refreshed token', async () => {
  vi.mocked(XMLHttpRequest.prototype.send).mockImplementation(function (
    this: XMLHttpRequest
  ) {
    finishRefresh(this, makeBundle(101, 'fresh-A'))
  })
  const observed: string[] = []
  api.defaults.adapter = async (config) => {
    observed.push(String(config.headers.get('Authorization')))
    if (!config.authRetry) throw unauthorized(config)
    return success(config)
  }
  await expect(
    api.post('/api/token/', { name: 'A private key' })
  ).resolves.toMatchObject({ status: 200 })
  expect(observed).toEqual(['Bearer synthetic-access-101', 'Bearer fresh-A'])
  expect(XMLHttpRequest.prototype.send).toHaveBeenCalledTimes(1)
})

test('same-session proactive token rotation does not discard an unrelated successful request', async () => {
  const started = deferred<void>()
  const release = deferred<void>()
  api.defaults.adapter = async (config) => {
    started.resolve()
    await release.promise
    return success(config)
  }
  const pending = api.get('/api/user/self', { disableDuplicate: true })
  await started.promise
  applyAuthBundle(makeBundle(101, 'rotated-A'), false)
  release.resolve()
  await expect(pending).resolves.toMatchObject({ status: 200 })
})

test('an aborted request waiting for refresh is not replayed', async () => {
  const refreshStarted = deferred<XMLHttpRequest>()
  vi.mocked(XMLHttpRequest.prototype.send).mockImplementation(function (
    this: XMLHttpRequest
  ) {
    refreshStarted.resolve(this)
  })
  const controller = new AbortController()
  const adapter = vi.fn(async (config: InternalAxiosRequestConfig) => {
    throw unauthorized(config)
  })
  api.defaults.adapter = adapter
  const pending = api.post(
    '/api/token/',
    {},
    { signal: controller.signal, skipErrorHandler: true }
  )
  const rejected = expect(pending).rejects.toMatchObject({
    code: 'ERR_CANCELED',
  })
  const xhr = await refreshStarted.promise
  controller.abort()
  finishRefresh(xhr, makeBundle(101, 'fresh-A'))
  await rejected
  expect(adapter).toHaveBeenCalledTimes(1)
})

test('a delayed retried A 401 does not clear B authentication', async () => {
  vi.mocked(XMLHttpRequest.prototype.send).mockImplementation(function (
    this: XMLHttpRequest
  ) {
    finishRefresh(this, makeBundle(101, 'fresh-A'))
  })
  const retryStarted = deferred<void>()
  const release = deferred<void>()
  const adapter = vi.fn(async (config: InternalAxiosRequestConfig) => {
    if (config.authRetry) {
      retryStarted.resolve()
      await release.promise
    }
    throw unauthorized(config)
  })
  api.defaults.adapter = adapter
  const pending = api.post('/api/token/', {}, { skipErrorHandler: true })
  const rejected = expect(pending).rejects.toMatchObject({
    code: 'ERR_CANCELED',
  })
  await retryStarted.promise
  applyAuthBundle(makeBundle(202), false)
  release.resolve()
  await rejected
  expect(adapter).toHaveBeenCalledTimes(2)
  expect(useAuthStore.getState().auth.user?.id).toBe(202)
})

test('a single-use request awaiting fresh headers is not sent under a newer session', async () => {
  const stale = makeBundle(101)
  stale.access_expires_at = 1
  applyAuthBundle(stale, false)
  const refreshStarted = deferred<XMLHttpRequest>()
  vi.mocked(XMLHttpRequest.prototype.send).mockImplementation(function (
    this: XMLHttpRequest
  ) {
    refreshStarted.resolve(this)
  })
  const adapter = vi.fn(async (config: InternalAxiosRequestConfig) =>
    success(config)
  )
  api.defaults.adapter = adapter
  const pending = api.post('/sensitive', {}, { singleUseAuthorization: true })
  const rejected = expect(pending).rejects.toMatchObject({
    code: 'ERR_CANCELED',
  })
  const xhr = await refreshStarted.promise
  applyAuthBundle(makeBundle(202), false)
  finishRefresh(xhr, makeBundle(101, 'old-refresh'))
  await rejected
  expect(adapter).not.toHaveBeenCalled()
  expect(useAuthStore.getState().auth.user?.id).toBe(202)
})

test('GET dedup does not reuse a previous login request even when the SID is reused', async () => {
  const firstStarted = deferred<void>()
  const releaseFirst = deferred<void>()
  const adapter = vi.fn(async (config: InternalAxiosRequestConfig) => {
    if (
      String(config.headers.get('Authorization')) ===
      'Bearer synthetic-access-101'
    ) {
      firstStarted.resolve()
      await releaseFirst.promise
    }
    return success(config, { token: config.headers.get('Authorization') })
  })
  api.defaults.adapter = adapter
  const oldRequest = api.get('/api/user/self')
  const oldRejected = expect(oldRequest).rejects.toMatchObject({
    code: 'ERR_CANCELED',
  })
  await firstStarted.promise
  clearAuthentication(false)
  applyAuthBundle(makeBundle(101, 'new-login'), false)
  const newRequest = api.get('/api/user/self')
  await expect(newRequest).resolves.toMatchObject({
    data: { token: 'Bearer new-login' },
  })
  releaseFirst.resolve()
  await oldRejected
  expect(adapter).toHaveBeenCalledTimes(2)
})

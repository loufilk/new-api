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
import { QueryClient } from '@tanstack/react-query'
import { isRedirect } from '@tanstack/react-router'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

let queryClient: QueryClient

beforeEach(() => {
  vi.resetModules()
  localStorage.clear()
  sessionStorage.clear()
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
})

afterEach(() => {
  queryClient.clear()
})

async function callRootGuard() {
  const { Route } = await import('@/routes/__root')
  const beforeLoad = Route.options.beforeLoad as unknown as (
    args: unknown
  ) => Promise<unknown>
  return beforeLoad({
    context: { queryClient },
    location: { pathname: '/dashboard', href: '/dashboard' },
  })
}

it('reuses the setup response fetched during locale loading without another network round trip', async () => {
  const { api } = await import('@/lib/http-client')
  const { setupStatusQueryOptions } = await import('../api')
  const requests: string[] = []
  api.defaults.adapter = async (config) => {
    requests.push(config.url ?? '')
    return {
      data: { success: true, data: { status: true } },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }

  await queryClient.prefetchQuery(setupStatusQueryOptions)
  await callRootGuard()

  expect(requests).toEqual(['/api/setup'])
  expect(
    queryClient.getQueryData(setupStatusQueryOptions.queryKey)
  ).toBeUndefined()
})

it('still redirects an uninitialized installation and checks again after setup finishes', async () => {
  const { api } = await import('@/lib/http-client')
  const { setupStatusQueryOptions } = await import('../api')
  let initialized = false
  const requests: string[] = []
  api.defaults.adapter = async (config) => {
    requests.push(config.url ?? '')
    return {
      data: { success: true, data: { status: initialized } },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }

  await queryClient.prefetchQuery(setupStatusQueryOptions)
  const redirect = await callRootGuard().catch((error: unknown) => error)
  expect(isRedirect(redirect)).toBe(true)
  expect((redirect as { options: { to: string } }).options.to).toBe('/setup')
  expect(requests).toEqual(['/api/setup'])

  initialized = true
  await expect(callRootGuard()).resolves.toBeUndefined()
  expect(requests).toEqual(['/api/setup', '/api/setup'])
})

it('keeps a failed startup prefetch recoverable without navigating away before the guard runs', async () => {
  const { AxiosError } = await import('axios')
  const { toast } = await import('sonner')
  const { api } = await import('@/lib/http-client')
  const { createAppQueryClient } = await import('@/lib/query-client')
  const { setupStatusQueryOptions } = await import('../api')
  const navigate = vi.fn()
  const notify = vi.spyOn(toast, 'error')
  queryClient.clear()
  queryClient = createAppQueryClient(navigate)
  api.defaults.adapter = async (config) => {
    throw new AxiosError('Unavailable', 'ERR_BAD_RESPONSE', config, undefined, {
      data: { success: false },
      status: 500,
      statusText: 'Unavailable',
      headers: {},
      config,
    })
  }

  await queryClient.prefetchQuery(setupStatusQueryOptions)
  expect(navigate).not.toHaveBeenCalled()
  expect(notify).not.toHaveBeenCalled()

  api.defaults.adapter = async (config) => ({
    data: { success: true, data: { status: true } },
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  })
  await expect(callRootGuard()).resolves.toBeUndefined()
  expect(
    queryClient.getQueryData(setupStatusQueryOptions.queryKey)
  ).toBeUndefined()
})

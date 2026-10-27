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
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { getStatus } from '@/lib/api'
import {
  ensureSharedStatus,
  prefetchStatusAtStartup,
  STATUS_CACHE_UPDATED_AT_KEY,
  STATUS_QUERY_KEY,
  STATUS_STALE_TIME,
} from '@/lib/status-query'

vi.mock('@/lib/api', async (importOriginal) => {
  const original = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...original,
    getStatus: vi.fn(),
  }
})

const mockedGetStatus = vi.mocked(getStatus)

function createQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
}

beforeEach(() => {
  window.localStorage.clear()
  mockedGetStatus.mockReset()
})

describe('shared status query', () => {
  test('deduplicates concurrent startup and route-guard requests', async () => {
    const queryClient = createQueryClient()
    const status = { system_name: 'Shared Status' }
    let resolveStatus: ((value: typeof status) => void) | undefined
    mockedGetStatus.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveStatus = resolve
        })
    )

    const startup = prefetchStatusAtStartup(queryClient)
    const routeGuard = ensureSharedStatus(queryClient)
    expect(mockedGetStatus).toHaveBeenCalledTimes(1)

    resolveStatus?.(status)
    await expect(Promise.all([startup, routeGuard])).resolves.toEqual([
      status,
      status,
    ])
    expect(queryClient.getQueryData(STATUS_QUERY_KEY)).toEqual(status)
    expect(JSON.parse(window.localStorage.getItem('status') ?? '{}')).toEqual(
      status
    )
    expect(
      Number(window.localStorage.getItem(STATUS_CACHE_UPDATED_AT_KEY))
    ).toBeGreaterThan(0)
    queryClient.clear()
  })

  test('hydrates a fresh local cache without a network request', async () => {
    const queryClient = createQueryClient()
    const status = { system_name: 'Cached Status' }
    window.localStorage.setItem('status', JSON.stringify(status))
    window.localStorage.setItem(STATUS_CACHE_UPDATED_AT_KEY, String(Date.now()))

    await expect(prefetchStatusAtStartup(queryClient)).resolves.toEqual(status)
    expect(mockedGetStatus).not.toHaveBeenCalled()
    expect(queryClient.getQueryData(STATUS_QUERY_KEY)).toEqual(status)
    queryClient.clear()
  })

  test('falls back to stale cached status when refresh fails', async () => {
    const queryClient = createQueryClient()
    const status = { system_name: 'Last Known Status' }
    window.localStorage.setItem('status', JSON.stringify(status))
    window.localStorage.setItem(
      STATUS_CACHE_UPDATED_AT_KEY,
      String(Date.now() - STATUS_STALE_TIME - 1)
    )
    mockedGetStatus.mockRejectedValue(new Error('network down'))

    await expect(ensureSharedStatus(queryClient)).resolves.toEqual(status)
    expect(mockedGetStatus).toHaveBeenCalledTimes(1)
    queryClient.clear()
  })
})

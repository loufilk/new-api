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
import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { useMarketModelMetrics } from '../use-market-metrics'

afterEach(() => {
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

test('anonymous overview never requests group detail, and a session change cannot reuse another group response', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/perf-metrics/summary') {
      return {
        data: {
          success: true,
          data: {
            models: [{ model_name: 'm', success_rate: 98 }],
            groups: [{ group: 'ignored' }],
          },
        },
      }
    }
    return {
      data: {
        success: true,
        data: { model_name: 'm', groups: [{ group: 'member-only' }] },
      },
    }
  })
  useAuthStore.getState().auth.reset('checking')
  const hook = renderHook(() => useMarketModelMetrics('m'), {
    wrapper: ({ children }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  })
  expect(get).not.toHaveBeenCalled()
  act(() => useAuthStore.getState().auth.setBootstrapState('complete'))
  await waitFor(() =>
    expect(hook.result.current.data?.data.summary?.success_rate).toBe(98)
  )
  expect(hook.result.current.data?.data.groups).toEqual([])
  expect(get.mock.calls.map(([url]) => url)).toEqual([
    '/api/perf-metrics/summary',
  ])
  act(() =>
    useAuthStore.setState((state) => ({
      auth: { ...state.auth, user: { id: 17, username: 'user17', role: 1 } },
    }))
  )
  await waitFor(() =>
    expect(hook.result.current.data?.data.groups).toEqual([
      { group: 'member-only' },
    ])
  )
  act(() => useAuthStore.getState().auth.reset('complete'))
  expect(hook.result.current.data?.data.groups).toEqual([])
  hook.unmount()
  client.clear()
})

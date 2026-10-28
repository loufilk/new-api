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
import { afterEach, describe, expect, test, vi } from 'vitest'

import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

const apiGet = vi.hoisted(() => vi.fn())

vi.mock('@/lib/api', () => ({
  api: { get: apiGet },
}))

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({ status: null }),
}))

const { usePricingData } = await import('../use-pricing-data')

function bundleFor(id: number, sid: string): AuthBundle {
  return {
    access_token: `token-${id}`,
    token_type: 'Bearer',
    access_expires_at: Math.floor(Date.now() / 1000) + 600,
    user: { id, username: `user-${id}`, role: 1 },
    session: {
      sid,
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'test',
      created_at: 100,
      last_active_at: 100,
      expires_at: 1000,
    },
  }
}

function renderPricingData() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return renderHook(() => usePricingData(), {
    wrapper: ({ children }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
  })
}

afterEach(() => {
  useAuthStore.getState().auth.reset('idle')
  apiGet.mockReset()
})

describe('pricing endpoint selection', () => {
  test('an anonymous visitor reads the public catalogue without credentials', async () => {
    apiGet.mockResolvedValue({ data: { success: true, data: [] } })
    useAuthStore.getState().auth.reset('idle')

    renderPricingData()

    await waitFor(() =>
      expect(apiGet).toHaveBeenCalledWith('/public-api/pricing', {
        withCredentials: false,
        preserveAuthorization: true,
        skipAuthRefresh: true,
      })
    )
    expect(apiGet.mock.calls.map((call) => call[0])).toEqual([
      '/public-api/pricing',
    ])
  })

  test('an authenticated user reads the dashboard catalogue', async () => {
    apiGet.mockResolvedValue({ data: { success: true, data: [] } })
    useAuthStore.getState().auth.setBundle(bundleFor(1, 'session-a'))

    renderPricingData()

    await waitFor(() => expect(apiGet).toHaveBeenCalledWith('/api/pricing'))
    expect(apiGet.mock.calls.map((call) => call[0])).toEqual(['/api/pricing'])
  })

  test('the identity probe suppresses requests until it resolves', async () => {
    apiGet.mockResolvedValue({ data: { success: true, data: [] } })
    useAuthStore.getState().auth.setBootstrapState('checking')

    const { rerender } = renderPricingData()
    await act(async () => {})
    expect(apiGet).not.toHaveBeenCalled()

    act(() => {
      useAuthStore.getState().auth.setBootstrapState('complete')
    })
    rerender()

    await waitFor(() =>
      expect(apiGet).toHaveBeenCalledWith(
        '/public-api/pricing',
        expect.anything()
      )
    )
  })

  test("a different session refetches instead of reusing another user's prices", async () => {
    apiGet.mockResolvedValue({ data: { success: true, data: [] } })
    useAuthStore.getState().auth.setBundle(bundleFor(1, 'session-a'))

    const { rerender } = renderPricingData()
    await waitFor(() => expect(apiGet).toHaveBeenCalledWith('/api/pricing'))

    useAuthStore.getState().auth.setBundle(bundleFor(2, 'session-b'))
    rerender()

    await waitFor(() => expect(apiGet).toHaveBeenCalledTimes(2))
    expect(apiGet.mock.calls.map((call) => call[0])).toEqual([
      '/api/pricing',
      '/api/pricing',
    ])
  })

  test('a failed private request is not downgraded to the public endpoint', async () => {
    apiGet.mockRejectedValue(new Error('unauthorized'))
    useAuthStore.getState().auth.setBundle(bundleFor(1, 'session-a'))

    const { result } = renderPricingData()

    await waitFor(() => expect(result.current.error).toBeTruthy())
    expect(apiGet.mock.calls.map((call) => call[0])).toEqual(['/api/pricing'])
  })
})

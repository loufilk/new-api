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
  focusManager,
  QueryClient,
  QueryClientProvider,
} from '@tanstack/react-query'
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

    await waitFor(() =>
      expect(apiGet).toHaveBeenCalledWith('/api/pricing', {
        signal: expect.any(AbortSignal),
        disableDuplicate: true,
      })
    )
    expect(apiGet.mock.calls.map((call) => call[0])).toEqual(['/api/pricing'])
  })

  test.each([true, false])(
    'per-model quota and price source are scoped to a real authenticated group (signed in: %s)',
    async (loggedIn) => {
      const policy = {
        window_minutes: 2,
        total_requests: 100,
        successful_requests: 0,
        scope: 'group',
      }
      apiGet.mockResolvedValue({
        data: {
          success: true,
          data: [
            {
              id: 1,
              model_name: 'm',
              enable_groups: ['allowed', 'not-usable'],
            },
          ],
          usable_group: { allowed: {}, unrelated: {} },
          group_rate_limits: {
            allowed: policy,
            'not-usable': policy,
            unrelated: policy,
          },
          group_rate_source: {
            allowed: 'personal',
            'not-usable': 'hidden',
            unrelated: 'unused',
          },
        },
      })
      if (loggedIn) {
        useAuthStore.getState().auth.setBundle(bundleFor(1, 'session-a'))
      }
      const { result } = renderPricingData()
      await waitFor(() => expect(result.current.models).toHaveLength(1))
      const model = result.current.models[0]
      expect(model.group_rate_limits).toEqual(
        loggedIn ? { allowed: policy } : undefined
      )
      expect(model.group_rate_source).toEqual(
        loggedIn ? { allowed: 'personal' } : undefined
      )
    }
  )

  test.each([true, false])(
    'never attaches another group or a personal price event to a model (signed in: %s)',
    async (signedIn) => {
      const activity = {
        first_configured_at: 1795521600,
        is_new: true,
        recent_rate_change: true,
        rate_changes: [
          { old_ratio: 0.5, new_ratio: 0, effective_at: 1795521700 },
        ],
      }
      const groups = [
        'base',
        'personal',
        'override',
        'stale',
        'unusable',
        'irrelevant',
        'bad',
        'unused',
      ]
      apiGet.mockResolvedValue({
        data: {
          success: true,
          data: [
            {
              id: 1,
              model_name: 'one',
              enable_groups: groups.filter(
                (g) => g !== 'irrelevant' && g !== 'unused'
              ),
            },
            { id: 2, model_name: 'two', enable_groups: ['irrelevant'] },
          ],
          usable_group: Object.fromEntries(
            groups.filter((g) => g !== 'unusable').map((g) => [g, {}])
          ),
          group_rate_source: {
            base: 'group',
            personal: 'personal',
            override: 'group_override',
            stale: 'group',
            irrelevant: 'group',
            bad: 'group',
          },
          group_ratio: {
            base: 0,
            personal: 0,
            override: 0,
            stale: 2,
            irrelevant: 0,
            bad: 0,
          },
          group_market_activity: {
            ...Object.fromEntries(groups.map((g) => [g, activity])),
            bad: {
              first_configured_at: 1795521600000,
              is_new: true,
              recent_rate_change: true,
              rate_changes: [{ old_ratio: 0, new_ratio: 0, effective_at: 0 }],
            },
          },
        },
      })
      if (signedIn) {
        useAuthStore.getState().auth.setBundle(bundleFor(1, 'session-a'))
      }
      const { result } = renderPricingData()
      await waitFor(() => expect(result.current.models).toHaveLength(2))
      const firstOnly = {
        first_configured_at: activity.first_configured_at,
        is_new: true,
      }
      expect(result.current.models[0].group_market_activity).toEqual(
        signedIn
          ? {
              base: activity,
              personal: firstOnly,
              override: firstOnly,
              stale: firstOnly,
            }
          : undefined
      )
      expect(result.current.groupMarketActivity).toEqual(
        signedIn
          ? {
              base: activity,
              irrelevant: activity,
              personal: firstOnly,
              override: firstOnly,
              stale: firstOnly,
            }
          : undefined
      )
    }
  )

  test('returning to the authenticated marketplace refetches a just-committed event even within the fresh interval', async () => {
    const activity = { first_configured_at: 1795521600, is_new: true }
    const baseline = {
      success: true,
      data: [{ id: 1, model_name: 'm', enable_groups: ['g'] }],
      usable_group: { g: {} },
    }
    apiGet.mockResolvedValueOnce({ data: baseline }).mockResolvedValue({
      data: { ...baseline, group_market_activity: { g: activity } },
    })
    useAuthStore.getState().auth.setBundle(bundleFor(1, 'session-a'))
    const { result, unmount } = renderPricingData()
    try {
      await waitFor(() => expect(result.current.models).toHaveLength(1))
      expect(result.current.groupMarketActivity).toBeUndefined()
      act(() => {
        focusManager.setFocused(false)
        focusManager.setFocused(true)
      })
      await waitFor(() =>
        expect(result.current.groupMarketActivity).toEqual({ g: activity })
      )
    } finally {
      unmount()
      focusManager.setFocused(undefined)
    }
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
    await waitFor(() =>
      expect(apiGet).toHaveBeenCalledWith('/api/pricing', {
        signal: expect.any(AbortSignal),
        disableDuplicate: true,
      })
    )

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

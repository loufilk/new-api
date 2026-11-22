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
import { createElement, type ReactNode } from 'react'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { useAffiliate } from '../use-affiliate'

describe('useAffiliate transfer retries', () => {
  beforeEach(() => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: 'aff-1' },
    })
  })

  async function renderAffiliate() {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const view = renderHook(() => useAffiliate(), {
      wrapper: (props: { children: ReactNode }) =>
        createElement(QueryClientProvider, { client }, props.children),
    })
    await waitFor(() => expect(view.result.current.loading).toBe(false))
    return view
  }

  test('moves released rewards using the browser session without a separate transaction challenge', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: { success: true } })
    const { result } = await renderAffiliate()
    await act(async () => {
      expect(await result.current.transferQuota(150000)).toBe(true)
    })
    expect(post).toHaveBeenCalledWith(
      '/api/user/aff_transfer',
      { quota: 150000 },
      {
        headers: { 'Idempotency-Key': expect.any(String) },
      }
    )
  })

  test('reuses the same idempotency key after a failed response and renews it after success', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockRejectedValueOnce(new Error('Network unavailable'))
      .mockResolvedValue({ data: { success: true } })
    const { result } = await renderAffiliate()
    await act(async () => {
      expect(await result.current.transferQuota(500000)).toBe(false)
    })
    await act(async () => {
      expect(await result.current.transferQuota(500000)).toBe(true)
    })
    await act(async () => {
      expect(await result.current.transferQuota(500000)).toBe(true)
    })
    const keys = post.mock.calls.map(
      (call) => call[2]?.headers?.['Idempotency-Key']
    )
    expect(keys[0]).toBe(keys[1])
    expect(keys[2]).not.toBe(keys[1])
  })

  test('rejects duplicate in-flight and unsafe amounts without another request', async () => {
    let finish!: (value: unknown) => void
    const post = vi.spyOn(api, 'post').mockReturnValue(
      new Promise((resolve) => {
        finish = resolve
      })
    )
    const { result } = await renderAffiliate()
    let transfer!: Promise<boolean>
    act(() => {
      transfer = result.current.transferQuota(500000)
    })
    expect(await result.current.transferQuota(1000000)).toBe(false)
    expect(
      await result.current.transferQuota(Number.MAX_SAFE_INTEGER + 1)
    ).toBe(false)
    expect(post).toHaveBeenCalledTimes(1)
    await act(async () => {
      finish({ data: { success: true } })
      expect(await transfer).toBe(true)
    })
  })
})

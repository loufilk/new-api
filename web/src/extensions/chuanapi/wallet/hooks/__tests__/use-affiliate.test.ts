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
import { act, renderHook, waitFor } from '@testing-library/react'
import { toast } from 'sonner'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { useAffiliate } from '../use-affiliate'

describe('ordinary invitation reward transfers', () => {
  beforeEach(() => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: 'aff-1' },
    })
  })

  async function renderAffiliate() {
    const view = renderHook(() => useAffiliate())
    await waitFor(() => expect(view.result.current.loading).toBe(false))
    return view
  }

  test('sends ordinary wallet quota with an idempotency key and no financial authorization proof', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: { success: true } })
    const { result } = await renderAffiliate()
    await act(async () => {
      expect(await result.current.transferQuota(500000)).toBe(true)
    })
    expect(post).toHaveBeenCalledExactlyOnceWith(
      '/api/user/aff_transfer',
      { quota: 500000 },
      { headers: { 'Idempotency-Key': expect.any(String) } }
    )
    expect(result.current.transferring).toBe(false)
  })

  test('reuses the same idempotency key when retrying after an interrupted response', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockRejectedValueOnce(new Error('connection lost'))
      .mockResolvedValueOnce({ data: { success: true } })
    const { result } = await renderAffiliate()
    await act(async () => {
      expect(await result.current.transferQuota(500000)).toBe(false)
    })
    await act(async () => {
      expect(await result.current.transferQuota(500000)).toBe(true)
    })
    expect(post.mock.calls[1][2]).toEqual(post.mock.calls[0][2])
  })

  test('starts a fresh idempotency key after a completed transfer', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: { success: true } })
    const { result } = await renderAffiliate()
    await act(async () => {
      await result.current.transferQuota(500000)
      await result.current.transferQuota(500000)
    })
    expect(post.mock.calls[1][2]?.headers?.['Idempotency-Key']).not.toBe(
      post.mock.calls[0][2]?.headers?.['Idempotency-Key']
    )
  })

  test('keeps a server business failure visible and does not treat it as a successful transfer', async () => {
    vi.spyOn(api, 'post').mockResolvedValue({
      data: { success: false, message: 'Insufficient available quota' },
    })
    const error = vi.spyOn(toast, 'error')
    const { result } = await renderAffiliate()
    await act(async () => {
      expect(await result.current.transferQuota(500000)).toBe(false)
    })
    expect(error).toHaveBeenCalledWith('Insufficient available quota')
    expect(result.current.transferring).toBe(false)
  })

  test.each([0, -1, 0.5, Number.MAX_SAFE_INTEGER + 1, Number.NaN])(
    'rejects invalid quota %s before making a financial request',
    async (quota) => {
      const post = vi.spyOn(api, 'post')
      const { result } = await renderAffiliate()
      await act(async () => {
        expect(await result.current.transferQuota(quota)).toBe(false)
      })
      expect(post).not.toHaveBeenCalled()
    }
  )

  test('rejects simultaneous transfers until the original request has completed', async () => {
    let finish!: (value: { data: { success: boolean } }) => void
    const post = vi.spyOn(api, 'post').mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve
        })
    )
    const { result } = await renderAffiliate()
    let pending!: Promise<boolean>
    act(() => {
      pending = result.current.transferQuota(500000)
    })
    expect(result.current.transferring).toBe(true)
    await act(async () => {
      expect(await result.current.transferQuota(1000000)).toBe(false)
    })
    expect(post).toHaveBeenCalledTimes(1)
    await act(async () => {
      finish({ data: { success: true } })
      expect(await pending).toBe(true)
    })
    expect(result.current.transferring).toBe(false)
  })
})

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
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { getAffiliateCode, transferAffiliateQuota } from '../../api'
import { useAffiliate } from '../use-affiliate'

vi.mock('../../api', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  getAffiliateCode: vi.fn(),
  transferAffiliateQuota: vi.fn(),
}))

describe('useAffiliate transfer idempotency', () => {
  beforeEach(() => {
    vi.mocked(getAffiliateCode).mockResolvedValue({
      success: true,
      data: 'aff-1',
    })
  })

  async function renderAffiliate() {
    const view = renderHook(() => useAffiliate())
    await waitFor(() => expect(view.result.current.loading).toBe(false))
    return view
  }

  test('reuses the idempotency key when the same amount is retried after a lost response', async () => {
    vi.mocked(transferAffiliateQuota)
      .mockRejectedValueOnce(new Error('connection lost'))
      .mockResolvedValueOnce({ success: true, message: 'Transfer successful' })

    const { result } = await renderAffiliate()

    let firstAttempt = true
    await act(async () => {
      firstAttempt = await result.current.transferQuota(500000)
    })
    expect(firstAttempt).toBe(false)

    let secondAttempt = false
    await act(async () => {
      secondAttempt = await result.current.transferQuota(500000)
    })
    expect(secondAttempt).toBe(true)

    const [firstCall, secondCall] = vi.mocked(transferAffiliateQuota).mock.calls
    expect(firstCall[0]).toEqual({ quota: 500000 })
    expect(secondCall[0]).toEqual({ quota: 500000 })
    expect(firstCall[1]).toBe(secondCall[1])
  })

  test('uses a new idempotency key when the amount changes after a failure', async () => {
    vi.mocked(transferAffiliateQuota)
      .mockRejectedValueOnce(new Error('connection lost'))
      .mockResolvedValueOnce({ success: true, message: 'Transfer successful' })

    const { result } = await renderAffiliate()

    await act(async () => {
      await result.current.transferQuota(500000)
    })
    await act(async () => {
      await result.current.transferQuota(1000000)
    })

    const [firstCall, secondCall] = vi.mocked(transferAffiliateQuota).mock.calls
    expect(firstCall[0]).toEqual({ quota: 500000 })
    expect(secondCall[0]).toEqual({ quota: 1000000 })
    expect(firstCall[1]).not.toBe(secondCall[1])
  })

  test('uses a fresh idempotency key for a new transfer after success', async () => {
    vi.mocked(transferAffiliateQuota).mockResolvedValue({
      success: true,
      message: 'Transfer successful',
    })

    const { result } = await renderAffiliate()

    await act(async () => {
      await result.current.transferQuota(500000)
    })
    await act(async () => {
      await result.current.transferQuota(500000)
    })

    const [firstCall, secondCall] = vi.mocked(transferAffiliateQuota).mock.calls
    expect(firstCall[1]).not.toBe(secondCall[1])
  })
})

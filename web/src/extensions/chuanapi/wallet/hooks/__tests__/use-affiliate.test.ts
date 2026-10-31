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

import { api } from '@/lib/api'

import { useAffiliate } from '../use-affiliate'

describe('useAffiliate transaction handoff', () => {
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
  test('does not transfer before a concrete transaction is authorized', async () => {
    const post = vi.spyOn(api, 'post')
    const { result } = await renderAffiliate()
    let pending!: Promise<boolean>
    act(() => {
      pending = result.current.transferQuota(500000)
    })
    expect(result.current.transferDraft).toMatchObject({
      action: 'commission.convert.legacy',
      payload: { quota: 500000 },
    })
    expect(post).not.toHaveBeenCalled()
    act(() => result.current.finishTransfer(false))
    expect(await pending).toBe(false)
  })
  test('retains the same request ID after an unresolved or cancelled attempt', async () => {
    const { result } = await renderAffiliate()
    act(() => {
      void result.current.transferQuota(500000)
    })
    const firstId = result.current.transferDraft?.request_id
    act(() => result.current.finishTransfer(false))
    act(() => {
      void result.current.transferQuota(500000)
    })
    expect(result.current.transferDraft?.request_id).toBe(firstId)
    act(() => result.current.finishTransfer(false))
  })
  test('allocates a fresh request only after completed transfer', async () => {
    const { result } = await renderAffiliate()
    act(() => {
      void result.current.transferQuota(500000)
    })
    const firstId = result.current.transferDraft?.request_id
    act(() => result.current.finishTransfer(true))
    act(() => {
      void result.current.transferQuota(500000)
    })
    expect(result.current.transferDraft?.request_id).not.toBe(firstId)
    act(() => result.current.finishTransfer(false))
  })
  test('rejects duplicates and unsafe quota without replacing the existing operation', async () => {
    const { result } = await renderAffiliate()
    expect(
      await result.current.transferQuota(Number.MAX_SAFE_INTEGER + 1)
    ).toBe(false)
    act(() => {
      void result.current.transferQuota(500000)
    })
    const firstId = result.current.transferDraft?.request_id
    expect(await result.current.transferQuota(1000000)).toBe(false)
    expect(result.current.transferDraft?.request_id).toBe(firstId)
    act(() => result.current.finishTransfer(false))
  })
})

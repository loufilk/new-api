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
import { expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  handleBatchDisableModels,
  handleBatchEnableModels,
} from '../lib/model-actions'

it.each([handleBatchEnableModels, handleBatchDisableModels])(
  'reconciles committed visibility changes after a sibling write loses its response',
  async (update) => {
    const client = new QueryClient()
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    const clearSelection = vi.fn()
    let confirm!: () => void
    const pending = new Promise<void>((resolve) => (confirm = resolve))
    const write = vi
      .spyOn(api, 'put')
      .mockRejectedValueOnce(new Error('Simulated response loss'))
      .mockImplementationOnce(async () => {
        await pending
        return { data: { success: true } }
      })
    const done = update([301, 302], client, clearSelection)
    expect(write).toHaveBeenCalledTimes(2)
    confirm()
    await done
    expect(write).toHaveBeenCalledTimes(2)
    for (const key of ['vendors', 'models', 'pricing']) {
      expect(invalidate).toHaveBeenCalledWith({ queryKey: [key] })
    }
    expect(clearSelection).not.toHaveBeenCalled()
  }
)

it('clears the selection only after every requested change is confirmed', async () => {
  const clearSelection = vi.fn()
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
  await handleBatchEnableModels([301, 302], new QueryClient(), clearSelection)
  expect(clearSelection).toHaveBeenCalledOnce()
})

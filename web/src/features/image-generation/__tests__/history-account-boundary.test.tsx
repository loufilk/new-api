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
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { IMAGE_HISTORY_STORAGE_KEY } from '../constants'
import { useImageHistory } from '../hooks/use-image-history'
import {
  deleteImageBlob,
  loadImageBlob,
  saveImageBlob,
} from '../lib/blob-store'
import { clearManagedObjectUrls } from '../lib/managed-object-urls'
import { imageHistoryStorageKey } from '../storage/history-owner'
import {
  loadHistoryEntries,
  saveHistoryEntries,
} from '../storage/history-storage'
import type { ImageHistoryEntry } from '../types'
import { signInHistoryOwner } from './history-fixture'

vi.mock('../lib/blob-store', () => ({
  deleteImageBlob: vi.fn(async () => undefined),
  loadImageBlob: vi.fn(async () => null),
  saveImageBlob: vi.fn(async () => undefined),
}))
function entry(id: string, kept = false): ImageHistoryEntry {
  return {
    id,
    prompt: `private ${id}`,
    model: 'test-image',
    settings: { size: '1024x1024', quality: 'auto', style: '', count: 1 },
    createdAt: Date.now(),
    results: [
      {
        id: `${id}-r`,
        type: kept ? 'blob' : 'url',
        source: kept ? 'same-key' : `https://images.example.invalid/${id}.png`,
        ...(kept ? { blobKey: 'same-key' } : {}),
        keptLocally: kept,
        createdAt: Date.now(),
      },
    ],
  }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
beforeEach(() => {
  localStorage.clear()
  signInHistoryOwner(101)
  vi.mocked(loadImageBlob).mockReset().mockResolvedValue(null)
  vi.mocked(saveImageBlob).mockReset().mockResolvedValue(undefined)
  vi.mocked(deleteImageBlob).mockReset().mockResolvedValue(undefined)
  URL.createObjectURL = vi.fn(() => 'blob:owned-image')
  URL.revokeObjectURL = vi.fn()
})
afterEach(() => {
  clearManagedObjectUrls()
  vi.unstubAllGlobals()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
})

test('metadata is retained A → logout → B → A without reading or claiming legacy data', () => {
  const legacy = JSON.stringify([entry('unowned-legacy')])
  localStorage.setItem(IMAGE_HISTORY_STORAGE_KEY, legacy)
  expect(loadHistoryEntries()).toEqual([])
  saveHistoryEntries([entry('A')])
  useAuthStore.getState().auth.reset()
  expect(loadHistoryEntries()).toEqual([])
  expect(saveHistoryEntries([entry('anonymous')])).toBe(false)
  signInHistoryOwner(202)
  expect(loadHistoryEntries()).toEqual([])
  saveHistoryEntries([entry('B')])
  signInHistoryOwner(101)
  expect(loadHistoryEntries().map((item) => item.id)).toEqual(['A'])
  expect(localStorage.getItem(imageHistoryStorageKey(202) ?? '')).toContain(
    'private B'
  )
  expect(localStorage.getItem(IMAGE_HISTORY_STORAGE_KEY)).toBe(legacy)
})

test('mounted hook revokes A URLs, hides A immediately, preserves both owners and resets filters', async () => {
  saveHistoryEntries([entry('A', true)])
  vi.mocked(loadImageBlob).mockResolvedValue(new Blob(['A']))
  const { result } = renderHook(() => useImageHistory())
  await act(async () => {})
  expect(result.current.resolveResultUrl('A-r', '')).toBe('blob:owned-image')
  const staleClear = result.current.clearAll
  act(() => {
    result.current.setQuery('private A')
    signInHistoryOwner(202)
  })
  expect(result.current.entries).toEqual([])
  expect(result.current.query).toBe('')
  expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:owned-image')
  expect(loadImageBlob).toHaveBeenCalledTimes(1)
  expect(loadImageBlob).toHaveBeenCalledWith('same-key', 101)
  expect(localStorage.getItem(imageHistoryStorageKey(202) ?? '')).not.toContain(
    'private A'
  )
  act(staleClear)
  expect(loadHistoryEntries(101)).toHaveLength(1)
  saveHistoryEntries([entry('B')])
  act(() => signInHistoryOwner(101))
  await act(async () => {})
  expect(result.current.entries.map((item) => item.id)).toEqual(['A'])
  expect(loadHistoryEntries(202).map((item) => item.id)).toEqual(['B'])
})

test('a delayed A blob read cannot create a URL or expose metadata in B', async () => {
  saveHistoryEntries([entry('A', true)])
  const read = deferred<Blob | null>()
  vi.mocked(loadImageBlob).mockReturnValue(read.promise)
  const { result } = renderHook(() => useImageHistory())
  act(() => signInHistoryOwner(202))
  await act(async () => {
    read.resolve(new Blob(['A']))
    await read.promise
  })
  expect(result.current.entries).toEqual([])
  expect(URL.createObjectURL).not.toHaveBeenCalled()
  expect(loadImageBlob).toHaveBeenCalledTimes(1)
})

test('switching accounts aborts A native fetch and suppresses stale completion and errors', async () => {
  saveHistoryEntries([entry('A')])
  const response = deferred<Response>()
  const fetchMock = vi.fn(() => response.promise)
  vi.stubGlobal('fetch', fetchMock)
  const { result } = renderHook(() => useImageHistory())
  let pending!: Promise<void>
  act(() => {
    pending = result.current.keepLocally('A', 'A-r')
  })
  const signal = (
    fetchMock.mock.calls[0] as unknown as [string, RequestInit]
  )[1].signal
  act(() => signInHistoryOwner(202))
  expect(signal?.aborted).toBe(true)
  await act(async () => {
    response.resolve(new Response('A'))
    await pending
  })
  expect(saveImageBlob).not.toHaveBeenCalled()
  expect(result.current.entries).toEqual([])
})

test('old keep rollback remains bound to A and cannot delete a newer keep after returning to A', async () => {
  saveHistoryEntries([entry('A')])
  const saved = deferred<void>()
  vi.mocked(saveImageBlob).mockReturnValueOnce(saved.promise)
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response('A'))
  )
  const { result } = renderHook(() => useImageHistory())
  let pending!: Promise<void>
  act(() => {
    pending = result.current.keepLocally('A', 'A-r')
  })
  await waitFor(() => expect(saveImageBlob).toHaveBeenCalledTimes(1))
  expect(await vi.mocked(saveImageBlob).mock.calls[0][1].text()).toBe('A')
  const oldKey = vi.mocked(saveImageBlob).mock.calls[0][0]
  act(() => useAuthStore.getState().auth.reset())
  act(() => signInHistoryOwner(202))
  expect(result.current.entries).toEqual([])
  act(() => signInHistoryOwner(101))
  await act(async () => {
    await result.current.keepLocally('A', 'A-r')
  })
  const newKey = vi.mocked(saveImageBlob).mock.calls[1][0]
  expect(newKey).not.toBe(oldKey)
  await act(async () => {
    saved.resolve()
    await pending
  })
  expect(deleteImageBlob).toHaveBeenCalledWith(oldKey, 101)
  expect(deleteImageBlob).not.toHaveBeenCalledWith(newKey, 101)
  expect(result.current.entries[0].results[0].blobKey).toBe(newKey)
})

test('a stale decoded-image callback cannot update a different owner or a later session', () => {
  saveHistoryEntries([entry('shared')], 101)
  saveHistoryEntries([entry('shared')], 202)
  const { result } = renderHook(() => useImageHistory())
  const staleRecord = result.current.recordDimensions
  act(() => signInHistoryOwner(202))
  act(() =>
    staleRecord(
      'shared',
      'shared-r',
      'https://images.example.invalid/shared.png',
      768,
      1344
    )
  )
  expect(result.current.entries[0].results[0].width).toBeUndefined()
  act(() => signInHistoryOwner(101))
  act(() =>
    staleRecord(
      'shared',
      'shared-r',
      'https://images.example.invalid/shared.png',
      768,
      1344
    )
  )
  expect(result.current.entries[0].results[0].width).toBeUndefined()
})

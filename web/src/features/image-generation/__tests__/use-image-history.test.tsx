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
import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { useImageHistory } from '../hooks/use-image-history'
import {
  deleteImageBlob,
  loadImageBlob,
  saveImageBlob,
} from '../lib/blob-store'
import { clearManagedObjectUrls } from '../lib/managed-object-urls'
import type { GeneratedImage } from '../types'
import {
  HISTORY_OWNER_ID,
  HISTORY_OWNER_KEY,
  signInHistoryOwner,
} from './history-fixture'

vi.mock('../lib/blob-store', () => ({
  deleteImageBlob: vi.fn(),
  loadImageBlob: vi.fn(),
  saveImageBlob: vi.fn(),
}))

function makeStoredResult(id: string, createdAt: number, blobKey?: string) {
  return {
    id,
    type: blobKey ? 'blob' : 'url',
    source: blobKey ?? 'https://cdn.example/a.png',
    ...(blobKey ? { blobKey } : {}),
    keptLocally: blobKey !== undefined,
    createdAt,
  }
}

function makeStoredEntry(id: string, createdAt: number, blobKey?: string) {
  return {
    id,
    prompt: `prompt ${id}`,
    model: 'dall-e-3',
    settings: {
      size: '1024x1024',
      quality: 'standard',
      style: 'vivid',
      count: 1,
    },
    createdAt,
    results: [makeStoredResult(`${id}-r`, createdAt, blobKey)],
  }
}

function seedStorage(entries: unknown[]) {
  window.localStorage.setItem(HISTORY_OWNER_KEY, JSON.stringify(entries))
}

function generatedImage(id: string): GeneratedImage {
  return {
    id,
    url: 'https://cdn.example/new.png',
    source: 'url',
    prompt: 'a new image',
    model: 'dall-e-3',
    createdAt: Date.now(),
  }
}

beforeEach(() => {
  signInHistoryOwner()
  window.localStorage.clear()
  clearManagedObjectUrls()
  vi.mocked(deleteImageBlob).mockResolvedValue(undefined)
  vi.mocked(loadImageBlob).mockResolvedValue(null)
  vi.mocked(saveImageBlob).mockResolvedValue(undefined)
  URL.createObjectURL = vi.fn(
    () => 'blob:kept-url'
  ) as typeof URL.createObjectURL
  URL.revokeObjectURL = vi.fn()
})

afterEach(() => {
  clearManagedObjectUrls()
  vi.unstubAllGlobals()
})

describe('useImageHistory keeps blob storage free of orphans', () => {
  test('evicting beyond 120 entries deletes the displaced IndexedDB blob', () => {
    const now = Date.now()
    seedStorage(
      Array.from({ length: 120 }, (_, index) =>
        makeStoredEntry(`entry-${index}`, now - (120 - index), `blob-${index}`)
      )
    )
    const { result } = renderHook(() => useImageHistory())
    expect(result.current.entries).toHaveLength(120)

    act(() => {
      result.current.pushEntry({
        prompt: 'a new image',
        model: 'dall-e-3',
        size: '1024x1024',
        quality: 'standard',
        style: 'vivid',
        count: 1,
        images: [generatedImage('img-new')],
      })
    })

    expect(result.current.entries).toHaveLength(120)
    expect(result.current.entries[0].prompt).toBe('a new image')
    expect(vi.mocked(deleteImageBlob)).toHaveBeenCalledWith(
      'blob-0',
      HISTORY_OWNER_ID
    )
  })

  test('removeEntry deletes the IndexedDB blob and revokes its object URL', async () => {
    const now = Date.now()
    seedStorage([makeStoredEntry('e1', now)])
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        blob: async () => new Blob(['image-bytes']),
      }))
    )
    const { result } = renderHook(() => useImageHistory())
    await act(async () => {
      await result.current.keepLocally('e1', 'e1-r')
    })
    expect(vi.mocked(saveImageBlob)).toHaveBeenCalledWith(
      expect.stringMatching(/^e1:e1-r:/),
      expect.any(Blob),
      HISTORY_OWNER_ID
    )
    expect(result.current.resolveResultUrl('e1-r', 'fallback')).toBe(
      'blob:kept-url'
    )

    act(() => {
      result.current.removeEntry('e1')
    })

    expect(result.current.entries).toEqual([])
    expect(vi.mocked(deleteImageBlob)).toHaveBeenCalledWith(
      expect.stringMatching(/^e1:e1-r:/),
      HISTORY_OWNER_ID
    )
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:kept-url')
    expect(result.current.resolveResultUrl('e1-r', 'fallback')).toBe('fallback')
  })

  test('clearAll releases every kept blob and empties the history', async () => {
    const now = Date.now()
    seedStorage([makeStoredEntry('e1', now)])
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        blob: async () => new Blob(['image-bytes']),
      }))
    )
    const { result } = renderHook(() => useImageHistory())
    await act(async () => {
      await result.current.keepLocally('e1', 'e1-r')
    })

    act(() => {
      result.current.clearAll()
    })

    expect(result.current.entries).toEqual([])
    expect(vi.mocked(deleteImageBlob)).toHaveBeenCalledWith(
      expect.stringMatching(/^e1:e1-r:/),
      HISTORY_OWNER_ID
    )
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:kept-url')
  })

  test('unmount revokes object URLs created during the session', async () => {
    const now = Date.now()
    seedStorage([makeStoredEntry('e1', now)])
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        blob: async () => new Blob(['image-bytes']),
      }))
    )
    const { result, unmount } = renderHook(() => useImageHistory())
    await act(async () => {
      await result.current.keepLocally('e1', 'e1-r')
    })
    expect(loadImageBlob).toBeDefined()

    unmount()

    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:kept-url')
  })
})

test('decoded dimensions replace legacy requested dimensions and drive persisted orientation', () => {
  const entry = makeStoredEntry('geometry', Date.now())
  seedStorage([
    { ...entry, results: [{ ...entry.results[0], width: 1024, height: 1024 }] },
  ])
  const { result } = renderHook(() => useImageHistory())
  act(() => result.current.setOrientation('square'))
  expect(result.current.visibleEntries).toEqual([])
  act(() =>
    result.current.recordDimensions(
      'geometry',
      'geometry-r',
      'https://cdn.example/a.png',
      768,
      1344
    )
  )
  act(() => result.current.setOrientation('portrait'))
  expect(result.current.visibleEntries).toHaveLength(1)
  expect(result.current.entries[0].settings.size).toBe('1024x1024')
  expect(
    JSON.parse(localStorage.getItem(HISTORY_OWNER_KEY) ?? '[]')[0].results[0]
  ).toMatchObject({ width: 768, height: 1344, dimensionsVerified: true })
})

test('stale and invalid decoded image dimensions cannot alter history', () => {
  seedStorage([makeStoredEntry('geometry', Date.now())])
  const { result } = renderHook(() => useImageHistory())
  act(() =>
    result.current.recordDimensions(
      'geometry',
      'geometry-r',
      'https://cdn.example/stale.png',
      768,
      1344
    )
  )
  act(() =>
    result.current.recordDimensions(
      'geometry',
      'geometry-r',
      'https://cdn.example/a.png',
      0,
      1344
    )
  )
  expect(result.current.entries[0].results[0].width).toBeUndefined()
  act(() => result.current.removeEntry('geometry'))
  act(() =>
    result.current.recordDimensions(
      'geometry',
      'geometry-r',
      'https://cdn.example/a.png',
      768,
      1344
    )
  )
  expect(result.current.entries).toEqual([])
})

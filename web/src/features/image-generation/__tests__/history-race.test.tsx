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
import { isSafeImageUrl } from '../lib/request-builders'
import {
  HISTORY_OWNER_ID,
  HISTORY_OWNER_KEY,
  signInHistoryOwner,
} from './history-fixture'

vi.mock('../lib/blob-store', () => ({
  deleteImageBlob: vi.fn(),
  loadImageBlob: vi.fn(async () => null),
  saveImageBlob: vi.fn(),
}))

function seedEntry(id: string) {
  const now = Date.now()
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
    createdAt: now,
    results: [
      {
        id: `${id}-r`,
        type: 'url',
        source: 'https://cdn.example/a.png',
        keptLocally: false,
        createdAt: now,
      },
    ],
  }
}

function seedKeptEntry(id: string) {
  const now = Date.now()
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
    createdAt: now,
    results: [
      {
        id: `${id}-r`,
        type: 'blob',
        source: `${id}:${id}-r`,
        blobKey: `${id}:${id}-r`,
        keptLocally: true,
        createdAt: now,
      },
    ],
  }
}

function seedStorage(entries: unknown[]) {
  window.localStorage.setItem(HISTORY_OWNER_KEY, JSON.stringify(entries))
}

function createdBlobUrl(): string | undefined {
  const calls = vi.mocked(URL.createObjectURL).mock.results
  const last = calls.at(-1)
  return last?.value as string | undefined
}

let resolveSave: ((value: undefined) => void) | null = null
let urlCounter = 0

beforeEach(() => {
  signInHistoryOwner()
  window.localStorage.clear()
  clearManagedObjectUrls()
  urlCounter = 0
  vi.mocked(deleteImageBlob).mockReset()
  vi.mocked(deleteImageBlob).mockResolvedValue(undefined)
  vi.mocked(loadImageBlob).mockReset()
  vi.mocked(loadImageBlob).mockResolvedValue(null)
  URL.createObjectURL = vi.fn(() => {
    urlCounter += 1
    return `blob:kept-${urlCounter}`
  }) as typeof URL.createObjectURL
  URL.revokeObjectURL = vi.fn()
  resolveSave = null
  vi.mocked(saveImageBlob).mockReset()
  vi.mocked(saveImageBlob).mockImplementation(
    () =>
      new Promise<undefined>((resolve) => {
        resolveSave = resolve as unknown as (value: undefined) => void
      })
  )
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({
      ok: true,
      blob: async () => new Blob(['image-bytes']),
    }))
  )
})

afterEach(() => {
  clearManagedObjectUrls()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('keepLocally does not orphan blobs when history changes mid-flight', () => {
  test('a failed metadata write rejects local retention and rolls back the saved blob', async () => {
    seedStorage([seedEntry('e1')])
    vi.mocked(saveImageBlob).mockResolvedValue(undefined)
    const { result } = renderHook(() => useImageHistory())
    vi.spyOn(window.localStorage, 'setItem').mockImplementation(() => {
      throw new DOMException('Storage full', 'QuotaExceededError')
    })
    await act(async () => {
      await expect(result.current.keepLocally('e1', 'e1-r')).rejects.toThrow(
        'keep-failed'
      )
    })
    expect(result.current.entries[0].results[0]).toMatchObject({
      keptLocally: false,
      type: 'url',
      source: 'https://cdn.example/a.png',
    })
    expect(deleteImageBlob).toHaveBeenCalledWith(
      expect.stringMatching(/^e1:e1-r:/),
      HISTORY_OWNER_ID
    )
    expect(result.current.resolveResultUrl('e1-r', 'fallback')).toBe('fallback')
  })

  test('removing the entry while persisting deletes the written blob and creates no URL', async () => {
    seedStorage([seedEntry('e1')])
    const { result } = renderHook(() => useImageHistory())
    expect(result.current.entries).toHaveLength(1)

    let keepPromise: Promise<void> | null = null
    act(() => {
      keepPromise = result.current.keepLocally('e1', 'e1-r')
    })
    expect(keepPromise).not.toBeNull()

    await act(async () => {
      await Promise.resolve()
    })
    expect(vi.mocked(saveImageBlob)).toHaveBeenCalledTimes(1)

    act(() => {
      result.current.removeEntry('e1')
    })
    expect(result.current.entries).toEqual([])

    await act(async () => {
      resolveSave?.(undefined)
      await keepPromise
    })

    expect(vi.mocked(deleteImageBlob)).toHaveBeenCalledWith(
      expect.stringMatching(/^e1:e1-r:/),
      HISTORY_OWNER_ID
    )
    expect(URL.createObjectURL).not.toHaveBeenCalled()
    expect(URL.revokeObjectURL).not.toHaveBeenCalled()
    expect(result.current.entries).toEqual([])
    expect(result.current.resolveResultUrl('e1-r', 'fallback')).toBe('fallback')
  })

  test('clearing history while persisting deletes the written blob and creates no URL', async () => {
    seedStorage([seedEntry('e1')])
    const { result } = renderHook(() => useImageHistory())

    let keepPromise: Promise<void> | null = null
    act(() => {
      keepPromise = result.current.keepLocally('e1', 'e1-r')
    })
    await act(async () => {
      await Promise.resolve()
    })

    act(() => {
      result.current.clearAll()
    })

    await act(async () => {
      resolveSave?.(undefined)
      await keepPromise
    })

    expect(result.current.entries).toEqual([])
    expect(vi.mocked(deleteImageBlob)).toHaveBeenCalledWith(
      expect.stringMatching(/^e1:e1-r:/),
      HISTORY_OWNER_ID
    )
    expect(URL.createObjectURL).not.toHaveBeenCalled()
    expect(URL.revokeObjectURL).not.toHaveBeenCalled()
    expect(result.current.resolveResultUrl('e1-r', 'fallback')).toBe('fallback')
  })

  test('unmounting while persisting deletes the written blob and creates no URL', async () => {
    seedStorage([seedEntry('e1')])
    const { result, unmount } = renderHook(() => useImageHistory())

    let keepPromise: Promise<void> | null = null
    act(() => {
      keepPromise = result.current.keepLocally('e1', 'e1-r')
    })
    await act(async () => {
      await Promise.resolve()
    })
    expect(vi.mocked(saveImageBlob)).toHaveBeenCalledTimes(1)

    unmount()

    await act(async () => {
      resolveSave?.(undefined)
      await keepPromise
    })

    expect(vi.mocked(deleteImageBlob)).toHaveBeenCalledWith(
      expect.stringMatching(/^e1:e1-r:/),
      HISTORY_OWNER_ID
    )
    expect(URL.createObjectURL).not.toHaveBeenCalled()
  })

  test('deleting inside the final commit window leaves no IDB blob or object URL', async () => {
    seedStorage([seedEntry('e1')])
    vi.mocked(saveImageBlob).mockResolvedValue(undefined)
    const { result } = renderHook(() => useImageHistory())

    vi.mocked(URL.createObjectURL).mockImplementation(() => {
      result.current.removeEntry('e1')
      urlCounter += 1
      return `blob:critical-${urlCounter}`
    })

    await act(async () => {
      await result.current.keepLocally('e1', 'e1-r')
    })

    const url = createdBlobUrl()
    expect(url).toBeDefined()
    expect(result.current.entries).toEqual([])
    expect(result.current.resolveResultUrl('e1-r', 'fallback')).toBe('fallback')
    expect(vi.mocked(deleteImageBlob)).toHaveBeenCalledWith(
      expect.stringMatching(/^e1:e1-r:/),
      HISTORY_OWNER_ID
    )
    expect(URL.revokeObjectURL).toHaveBeenCalledWith(url)
    if (url) expect(isSafeImageUrl(url)).toBe(false)
  })

  test('a failed persist cleans the partial write and keeps metadata unchanged', async () => {
    seedStorage([seedEntry('e1')])
    vi.mocked(saveImageBlob).mockRejectedValueOnce(new Error('quota exceeded'))
    const { result } = renderHook(() => useImageHistory())

    await act(async () => {
      await expect(result.current.keepLocally('e1', 'e1-r')).rejects.toThrow(
        'keep-failed'
      )
    })

    expect(result.current.entries).toHaveLength(1)
    expect(result.current.entries[0].results[0].keptLocally).toBe(false)
    expect(vi.mocked(deleteImageBlob)).not.toHaveBeenCalledWith(
      expect.stringMatching(/^e1:e1-r:/),
      HISTORY_OWNER_ID
    )
    expect(URL.createObjectURL).not.toHaveBeenCalled()
    expect(URL.revokeObjectURL).not.toHaveBeenCalled()
  })

  test('deduplicates concurrent keeps for the same result without deleting the winner', async () => {
    seedStorage([seedEntry('e1')])
    const { result } = renderHook(() => useImageHistory())

    let firstKeep: Promise<void> | null = null
    let secondKeep: Promise<void> | null = null
    act(() => {
      firstKeep = result.current.keepLocally('e1', 'e1-r')
      secondKeep = result.current.keepLocally('e1', 'e1-r')
    })

    await act(async () => {
      await Promise.resolve()
    })

    expect(vi.mocked(fetch)).toHaveBeenCalledTimes(1)
    expect(vi.mocked(saveImageBlob)).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveSave?.(undefined)
      await Promise.all([firstKeep, secondKeep])
    })

    expect(result.current.entries[0].results[0].keptLocally).toBe(true)
    expect(vi.mocked(deleteImageBlob)).not.toHaveBeenCalledWith(
      expect.stringMatching(/^e1:e1-r:/),
      HISTORY_OWNER_ID
    )
    expect(URL.createObjectURL).toHaveBeenCalledTimes(1)
  })

  test('releases the keep guard after a failed persistence so retry can succeed', async () => {
    seedStorage([seedEntry('e1')])
    vi.mocked(saveImageBlob)
      .mockRejectedValueOnce(new Error('quota exceeded'))
      .mockResolvedValueOnce(undefined)
    const { result } = renderHook(() => useImageHistory())

    await act(async () => {
      await expect(result.current.keepLocally('e1', 'e1-r')).rejects.toThrow(
        'keep-failed'
      )
    })

    await act(async () => {
      await result.current.keepLocally('e1', 'e1-r')
    })

    expect(vi.mocked(saveImageBlob)).toHaveBeenCalledTimes(2)
    expect(result.current.entries[0].results[0].keptLocally).toBe(true)
  })

  test('keeping a temporary base64 result revokes the replaced object URL', async () => {
    const { result } = renderHook(() => useImageHistory())

    act(() => {
      result.current.pushEntry({
        prompt: 'base64 image',
        model: 'gpt-image-1',
        size: '1024x1024',
        quality: 'medium',
        style: '',
        count: 1,
        images: [
          {
            id: 'generated-b64',
            url: 'data:image/png;base64,aW1hZ2UtYnl0ZXM=',
            source: 'b64',
            prompt: 'base64 image',
            model: 'gpt-image-1',
            createdAt: Date.now(),
          },
        ],
      })
    })

    const entry = result.current.entries[0]
    const resultId = entry.results[0].id
    const temporaryUrl = result.current.resolveResultUrl(
      resultId,
      entry.results[0].source
    )
    expect(temporaryUrl).toBe('blob:kept-1')
    expect(isSafeImageUrl(temporaryUrl)).toBe(true)

    let keepPromise: Promise<void> | null = null
    act(() => {
      keepPromise = result.current.keepLocally(entry.id, resultId)
    })
    await act(async () => {
      await Promise.resolve()
    })

    await act(async () => {
      resolveSave?.(undefined)
      await keepPromise
    })

    const committedUrl = result.current.resolveResultUrl(resultId, 'fallback')
    expect(committedUrl).toBe('blob:kept-2')
    expect(URL.revokeObjectURL).toHaveBeenCalledWith(temporaryUrl)
    expect(isSafeImageUrl(temporaryUrl)).toBe(false)
    expect(isSafeImageUrl(committedUrl)).toBe(true)
    expect(result.current.entries[0].results[0].keptLocally).toBe(true)
  })
})

describe('restoreBlobUrls does not orphan object URLs when history changes mid-load', () => {
  test('deleting before the IndexedDB load resolves creates no object URL', async () => {
    seedStorage([seedKeptEntry('e1')])
    let resolveLoad: ((value: Blob | null) => void) | null = null
    vi.mocked(loadImageBlob).mockImplementation(
      () =>
        new Promise<Blob | null>((resolve) => {
          resolveLoad = resolve
        })
    )
    const { result } = renderHook(() => useImageHistory())

    await act(async () => {
      await Promise.resolve()
    })
    expect(vi.mocked(loadImageBlob)).toHaveBeenCalledWith(
      'e1:e1-r',
      HISTORY_OWNER_ID
    )

    act(() => {
      result.current.removeEntry('e1')
    })
    expect(result.current.entries).toEqual([])

    await act(async () => {
      resolveLoad?.(new Blob(['restored-bytes']))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(URL.createObjectURL).not.toHaveBeenCalled()
    expect(result.current.resolveResultUrl('e1-r', 'fallback')).toBe('fallback')
  })

  test('unmounting before the IndexedDB load resolves creates no object URL', async () => {
    seedStorage([seedKeptEntry('e1')])
    let resolveLoad: ((value: Blob | null) => void) | null = null
    vi.mocked(loadImageBlob).mockImplementation(
      () =>
        new Promise<Blob | null>((resolve) => {
          resolveLoad = resolve
        })
    )
    const { unmount } = renderHook(() => useImageHistory())

    await act(async () => {
      await Promise.resolve()
    })
    expect(vi.mocked(loadImageBlob)).toHaveBeenCalledWith(
      'e1:e1-r',
      HISTORY_OWNER_ID
    )

    unmount()

    await act(async () => {
      resolveLoad?.(new Blob(['restored-bytes']))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(URL.createObjectURL).not.toHaveBeenCalled()
  })

  test('deleting right after restore revokes the restored URL immediately', async () => {
    seedStorage([seedKeptEntry('e1')])
    vi.mocked(loadImageBlob).mockResolvedValue(new Blob(['restored-bytes']))
    const { result } = renderHook(() => useImageHistory())

    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })

    const url = createdBlobUrl()
    expect(url).toBeDefined()
    expect(result.current.resolveResultUrl('e1-r', 'fallback')).toBe(url)
    if (url) expect(isSafeImageUrl(url)).toBe(true)

    act(() => {
      result.current.removeEntry('e1')
    })

    expect(result.current.entries).toEqual([])
    expect(URL.revokeObjectURL).toHaveBeenCalledWith(url)
    expect(result.current.resolveResultUrl('e1-r', 'fallback')).toBe('fallback')
    if (url) expect(isSafeImageUrl(url)).toBe(false)
  })
})

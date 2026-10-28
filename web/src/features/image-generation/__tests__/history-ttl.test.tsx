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

import { IMAGE_HISTORY_TTL_MS } from '../constants'
import { useImageHistory } from '../hooks/use-image-history'
import { deleteImageBlob } from '../lib/blob-store'
import { clearManagedObjectUrls } from '../lib/managed-object-urls'
import type { GeneratedImage } from '../types'
import { HISTORY_OWNER_KEY, signInHistoryOwner } from './history-fixture'

vi.mock('../lib/blob-store', () => ({
  deleteImageBlob: vi.fn(),
  loadImageBlob: vi.fn(async () => null),
  saveImageBlob: vi.fn(async () => undefined),
}))

function tempEntry(id: string, createdAt: number) {
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
    results: [
      {
        id: `${id}-r`,
        type: 'url',
        source: 'https://cdn.example/a.png',
        keptLocally: false,
        createdAt,
      },
    ],
  }
}

function keptEntry(id: string, createdAt: number) {
  const entry = tempEntry(id, createdAt)
  return {
    ...entry,
    results: [
      {
        ...entry.results[0],
        type: 'blob',
        source: `${id}:${id}-r`,
        blobKey: `${id}:${id}-r`,
        keptLocally: true,
      },
    ],
  }
}

beforeEach(() => {
  signInHistoryOwner()
  window.localStorage.clear()
  clearManagedObjectUrls()
  vi.mocked(deleteImageBlob).mockResolvedValue(undefined)
  URL.createObjectURL = vi.fn(
    () => 'blob:temp-url'
  ) as typeof URL.createObjectURL
  URL.revokeObjectURL = vi.fn()
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
})

afterEach(() => {
  vi.useRealTimers()
  clearManagedObjectUrls()
  vi.unstubAllGlobals()
  window.localStorage.clear()
})

describe('temporary history expires from React state on a long-open page', () => {
  test('the one-minute sweep drops expired temp entries but keeps local blobs', () => {
    const now = Date.now()
    window.localStorage.setItem(
      HISTORY_OWNER_KEY,
      JSON.stringify([tempEntry('fresh', now), keptEntry('kept', now)])
    )
    const { result } = renderHook(() => useImageHistory())
    expect(result.current.entries).toHaveLength(2)

    act(() => {
      vi.setSystemTime(new Date(now + IMAGE_HISTORY_TTL_MS + 1000))
      vi.advanceTimersByTime(60_000)
    })

    expect(result.current.entries.map((entry) => entry.id)).toEqual(['kept'])
    expect(window.localStorage.getItem(HISTORY_OWNER_KEY)).not.toContain(
      'fresh'
    )
  })

  test('pushing a new entry opportunistically prunes already-expired temp entries', () => {
    const now = Date.now()
    window.localStorage.setItem(
      HISTORY_OWNER_KEY,
      JSON.stringify([tempEntry('old', now)])
    )
    const { result } = renderHook(() => useImageHistory())
    expect(result.current.entries).toHaveLength(1)

    act(() => {
      vi.setSystemTime(new Date(now + IMAGE_HISTORY_TTL_MS + 5000))
    })

    const image: GeneratedImage = {
      id: 'new-img',
      url: 'https://cdn.example/new.png',
      source: 'url',
      prompt: 'a new image',
      model: 'dall-e-3',
      createdAt: Date.now(),
    }
    act(() => {
      result.current.pushEntry({
        prompt: 'a new image',
        model: 'dall-e-3',
        size: '1024x1024',
        quality: 'standard',
        style: 'vivid',
        count: 1,
        images: [image],
      })
    })

    expect(result.current.entries.map((entry) => entry.id)).not.toContain('old')
    expect(result.current.entries[0].prompt).toBe('a new image')
  })

  test('converting b64 history to object URLs avoids keeping base64 strings in state', () => {
    const { result } = renderHook(() => useImageHistory())
    const image: GeneratedImage = {
      id: 'b64-img',
      url: 'data:image/png;base64,aGVsbG8=',
      source: 'b64',
      prompt: 'a lake',
      model: 'gpt-image-1',
      createdAt: Date.now(),
    }

    act(() => {
      result.current.pushEntry({
        prompt: 'a lake',
        model: 'gpt-image-1',
        size: '1024x1024',
        quality: 'medium',
        style: '',
        count: 1,
        images: [image],
      })
    })

    const stored = result.current.entries[0].results[0]
    expect(stored.type).toBe('b64')
    expect(stored.source).toBe('blob:temp-url')
    expect(URL.createObjectURL).toHaveBeenCalled()
    expect(window.localStorage.getItem(HISTORY_OWNER_KEY) ?? '').not.toContain(
      'aGVsbG8='
    )
    expect(result.current.resolveResultUrl(stored.id, 'fallback')).toBe(
      'blob:temp-url'
    )
  })

  test('drops unregistered blob URLs from persisted history before rendering', () => {
    const now = Date.now()
    const polluted = tempEntry('polluted', now)
    polluted.results[0].source = 'blob:https://app.example/old'
    polluted.results.push({
      ...polluted.results[0],
      id: 'polluted-safe-r',
      source: 'https://cdn.example/safe.png',
    })
    window.localStorage.setItem(HISTORY_OWNER_KEY, JSON.stringify([polluted]))

    const { result } = renderHook(() => useImageHistory())

    expect(result.current.entries).toHaveLength(1)
    expect(result.current.entries[0].results).toHaveLength(1)
    expect(result.current.entries[0].results[0].source).toBe(
      'https://cdn.example/safe.png'
    )
  })

  test('expiring a converted temp b64 revokes its object URL', () => {
    const { result } = renderHook(() => useImageHistory())
    const start = Date.now()
    const image: GeneratedImage = {
      id: 'b64-img',
      url: 'data:image/png;base64,aGVsbG8=',
      source: 'b64',
      prompt: 'a lake',
      model: 'gpt-image-1',
      createdAt: start,
    }
    act(() => {
      result.current.pushEntry({
        prompt: 'a lake',
        model: 'gpt-image-1',
        size: '1024x1024',
        quality: 'medium',
        style: '',
        count: 1,
        images: [image],
      })
    })
    expect(result.current.entries).toHaveLength(1)

    act(() => {
      vi.setSystemTime(new Date(start + IMAGE_HISTORY_TTL_MS + 1000))
      vi.advanceTimersByTime(60_000)
    })

    expect(result.current.entries).toEqual([])
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:temp-url')
  })
})

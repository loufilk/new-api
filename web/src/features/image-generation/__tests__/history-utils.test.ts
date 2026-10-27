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
import { describe, expect, test } from 'vitest'

import { IMAGE_HISTORY_MAX_ENTRIES, IMAGE_HISTORY_TTL_MS } from '../constants'
import {
  filterHistoryEntries,
  isValidHistoryEntry,
  pruneHistoryEntries,
} from '../lib/history-utils'
import type { ImageHistoryEntry } from '../types'

function makeEntry(
  overrides: Partial<ImageHistoryEntry> = {}
): ImageHistoryEntry {
  const now = Date.now()
  return {
    id: `entry-${Math.random().toString(36).slice(2, 8)}`,
    prompt: 'a lake at sunrise',
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
        id: 'result-1',
        type: 'url',
        source: 'https://cdn.example/a.png',
        keptLocally: false,
        createdAt: now,
      },
    ],
    ...overrides,
  }
}

describe('history schema validation drops corrupt records', () => {
  test('rejects entries missing required fields or with bad results', () => {
    expect(isValidHistoryEntry(null)).toBe(false)
    expect(isValidHistoryEntry({ id: 'x' })).toBe(false)
    expect(isValidHistoryEntry(makeEntry())).toBe(true)
    expect(
      isValidHistoryEntry(makeEntry({ results: [{ bad: true } as never] }))
    ).toBe(false)
  })

  test('rejects non-string settings fields and non-positive counts', () => {
    expect(
      isValidHistoryEntry(
        makeEntry({
          settings: { size: 1024, quality: 's', style: 'v', count: 1 } as never,
        })
      )
    ).toBe(false)
    for (const count of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(
        isValidHistoryEntry(
          makeEntry({
            settings: { size: '1024x1024', quality: 's', style: 'v', count },
          })
        )
      ).toBe(false)
    }
  })

  test('rejects results with unknown types, empty sources, or bad timestamps', () => {
    const now = Date.now()
    const baseResult = {
      id: 'r1',
      type: 'url',
      source: 'https://x/y.png',
      keptLocally: false,
      createdAt: now,
    }
    expect(
      isValidHistoryEntry(
        makeEntry({ results: [{ ...baseResult, type: 'gif' } as never] })
      )
    ).toBe(false)
    expect(
      isValidHistoryEntry(
        makeEntry({ results: [{ ...baseResult, source: '' } as never] })
      )
    ).toBe(false)
    expect(
      isValidHistoryEntry(
        makeEntry({
          results: [{ ...baseResult, createdAt: Number.NaN } as never],
        })
      )
    ).toBe(false)
    expect(
      isValidHistoryEntry(
        makeEntry({ results: [{ ...baseResult, keptLocally: 'no' } as never] })
      )
    ).toBe(false)
  })

  test('rejects non-positive dimensions but keeps valid ones', () => {
    const now = Date.now()
    expect(
      isValidHistoryEntry(
        makeEntry({
          results: [
            {
              id: 'r1',
              type: 'url',
              source: 'https://x/y.png',
              width: 0,
              height: 1024,
              keptLocally: false,
              createdAt: now,
            },
          ],
        })
      )
    ).toBe(false)
    expect(
      isValidHistoryEntry(
        makeEntry({
          results: [
            {
              id: 'r1',
              type: 'url',
              source: 'https://x/y.png',
              width: 1024,
              height: Number.NaN,
              keptLocally: false,
              createdAt: now,
            } as never,
          ],
        })
      )
    ).toBe(false)
    expect(
      isValidHistoryEntry(
        makeEntry({
          results: [
            {
              id: 'r1',
              type: 'url',
              source: 'https://x/y.png',
              width: 1024,
              height: 1024,
              keptLocally: false,
              createdAt: now,
            },
          ],
        })
      )
    ).toBe(true)
  })

  test('rejects blob results that are not kept locally with a blob key', () => {
    const now = Date.now()
    expect(
      isValidHistoryEntry(
        makeEntry({
          results: [
            {
              id: 'r1',
              type: 'blob',
              source: 'blob-key',
              keptLocally: false,
              createdAt: now,
            } as never,
          ],
        })
      )
    ).toBe(false)
    expect(
      isValidHistoryEntry(
        makeEntry({
          results: [
            {
              id: 'r1',
              type: 'blob',
              source: 'blob-key',
              keptLocally: true,
              createdAt: now,
            } as never,
          ],
        })
      )
    ).toBe(false)
  })

  test('never stores API keys in history metadata', () => {
    const entry = makeEntry()
    expect(JSON.stringify(entry)).not.toContain('sk-')
    expect('apiKey' in entry).toBe(false)
  })
})

test('rejects unsafe image sources and inconsistent kept-local metadata', () => {
  const now = Date.now()
  const makeResult = (overrides: Record<string, unknown>) =>
    ({
      id: 'r1',
      type: 'url',
      source: 'https://cdn.example/a.png',
      keptLocally: false,
      createdAt: now,
      ...overrides,
    }) as ImageHistoryEntry['results'][number]
  expect(
    isValidHistoryEntry(
      makeEntry({ results: [makeResult({ source: 'javascript:alert(1)' })] })
    )
  ).toBe(false)
  expect(
    isValidHistoryEntry(
      makeEntry({ results: [makeResult({ keptLocally: true })] })
    )
  ).toBe(false)
  expect(
    isValidHistoryEntry(
      makeEntry({
        results: [
          makeResult({
            type: 'blob',
            source: 'https://cdn.example/a.png',
            keptLocally: true,
            blobKey: 'entry:result',
          }),
        ],
      })
    )
  ).toBe(false)
})

describe('pruneHistoryEntries enforces expiry and the 120-group cap', () => {
  test('drops temporary url records older than one hour but keeps local blobs', () => {
    const now = Date.now()
    const expiredUrl = makeEntry({
      id: 'expired',
      createdAt: now - IMAGE_HISTORY_TTL_MS - 1000,
      results: [
        {
          id: 'r1',
          type: 'url',
          source: 'https://x/y.png',
          keptLocally: false,
          createdAt: now - IMAGE_HISTORY_TTL_MS - 1000,
        },
      ],
    })
    const keptBlob = makeEntry({
      id: 'kept',
      createdAt: now - IMAGE_HISTORY_TTL_MS - 1000,
      results: [
        {
          id: 'r2',
          type: 'blob',
          source: 'k',
          blobKey: 'k',
          keptLocally: true,
          createdAt: now - IMAGE_HISTORY_TTL_MS - 1000,
        },
      ],
    })
    const pruned = pruneHistoryEntries([expiredUrl, keptBlob], now)
    expect(pruned.map((entry) => entry.id)).toEqual(['kept'])
  })

  test('caps history at 120 groups with newest first', () => {
    const now = Date.now()
    const entries = Array.from(
      { length: IMAGE_HISTORY_MAX_ENTRIES + 5 },
      (_, index) => makeEntry({ id: `entry-${index}`, createdAt: now - index })
    )
    const pruned = pruneHistoryEntries(entries, now)
    expect(pruned).toHaveLength(IMAGE_HISTORY_MAX_ENTRIES)
    expect(pruned[0].id).toBe('entry-0')
  })
})

describe('filterHistoryEntries supports search and orientation filters', () => {
  test('matches prompt/model text and separates orientations', () => {
    const square = makeEntry({
      id: 'square',
      prompt: 'minimal mountain poster',
      results: [
        {
          id: 's1',
          type: 'url',
          source: 'u',
          width: 1024,
          height: 1024,
          dimensionsVerified: true,
          keptLocally: false,
          createdAt: Date.now(),
        },
      ],
    })
    const landscape = makeEntry({
      id: 'wide',
      prompt: 'city street',
      model: 'gpt-image-2',
      results: [
        {
          id: 'w1',
          type: 'url',
          source: 'u',
          width: 1536,
          height: 1024,
          dimensionsVerified: true,
          keptLocally: false,
          createdAt: Date.now(),
        },
      ],
    })
    expect(
      filterHistoryEntries([square, landscape], 'mountain', 'all')
    ).toHaveLength(1)
    expect(
      filterHistoryEntries([square, landscape], 'gpt-image', 'all')[0].id
    ).toBe('wide')
    expect(
      filterHistoryEntries([square, landscape], '', 'square').map(
        (entry) => entry.id
      )
    ).toEqual(['square'])
    expect(filterHistoryEntries([square, landscape], '', 'portrait')).toEqual(
      []
    )
  })

  test('does not filter by requested size when actual dimensions are unknown', () => {
    const sized = makeEntry({
      id: 'sized-fallback',
      settings: {
        size: '1536x1024',
        quality: 'standard',
        style: 'vivid',
        count: 1,
      },
      results: [
        {
          id: 'f1',
          type: 'url',
          source: 'u',
          keptLocally: false,
          createdAt: Date.now(),
        },
      ],
    })
    const unsized = makeEntry({
      id: 'unsized',
      settings: { size: 'auto', quality: '', style: '', count: 1 },
      results: [
        {
          id: 'u1',
          type: 'url',
          source: 'u',
          keptLocally: false,
          createdAt: Date.now(),
        },
      ],
    })
    expect(
      filterHistoryEntries([sized, unsized], '', 'landscape').map(
        (entry) => entry.id
      )
    ).toEqual([])
    expect(filterHistoryEntries([sized, unsized], '', 'square')).toEqual([])
  })
})

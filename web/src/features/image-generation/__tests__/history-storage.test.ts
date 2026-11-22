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
import { beforeEach, describe, expect, test } from 'vitest'

import {
  clearHistoryStorage,
  loadHistoryEntries,
  saveHistoryEntries,
} from '../storage/history-storage'
import { HISTORY_OWNER_KEY, signInHistoryOwner } from './history-fixture'

function makeStoredEntry(id: string, createdAt: number) {
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

describe('history storage degrades safely on corrupt data', () => {
  beforeEach(() => {
    signInHistoryOwner()
    window.localStorage.clear()
  })

  test('returns empty for missing, non-array, or malformed payloads', () => {
    expect(loadHistoryEntries()).toEqual([])
    window.localStorage.setItem(HISTORY_OWNER_KEY, '{"nope":true}')
    expect(loadHistoryEntries()).toEqual([])
    window.localStorage.setItem(HISTORY_OWNER_KEY, 'not-json{{{')
    expect(loadHistoryEntries()).toEqual([])
  })

  test('round-trips valid entries and clears on demand', () => {
    const now = Date.now()
    saveHistoryEntries([makeStoredEntry('a', now) as never])
    expect(loadHistoryEntries()).toHaveLength(1)
    clearHistoryStorage()
    expect(loadHistoryEntries()).toEqual([])
  })

  test('does not persist unkept base64 image payloads in localStorage', () => {
    const now = Date.now()
    const entry = makeStoredEntry('b64', now)
    entry.results[0] = {
      ...entry.results[0],
      type: 'b64',
      source: 'data:image/png;base64,aGVsbG8=',
    }
    saveHistoryEntries([entry as never])
    expect(window.localStorage.getItem(HISTORY_OWNER_KEY)).not.toContain(
      'aGVsbG8='
    )
    expect(loadHistoryEntries()).toEqual([])
  })
})

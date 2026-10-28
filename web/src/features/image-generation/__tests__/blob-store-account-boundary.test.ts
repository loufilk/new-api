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
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import {
  deleteImageBlob,
  loadImageBlob,
  saveImageBlob,
} from '../lib/blob-store'
import { signInHistoryOwner } from './history-fixture'

// Replace only the IndexedDB I/O boundary; production namespace selection is real.
const blobs = new Map<string, Blob>()
const open = vi.fn(() => {
  const request = new EventTarget() as EventTarget & { result: unknown }
  request.result = {
    transaction: () => {
      const tx = new EventTarget() as EventTarget & {
        objectStore: () => unknown
      }
      tx.objectStore = () => ({
        put: (blob: Blob, key: string) => {
          blobs.set(key, blob)
          queueMicrotask(() => tx.dispatchEvent(new Event('complete')))
        },
        delete: (key: string) => {
          blobs.delete(key)
          queueMicrotask(() => tx.dispatchEvent(new Event('complete')))
        },
        get: (key: string) => {
          const read = new EventTarget() as EventTarget & { result?: Blob }
          read.result = blobs.get(key)
          queueMicrotask(() => read.dispatchEvent(new Event('success')))
          return read
        },
      })
      return tx
    },
    close: () => {},
  }
  queueMicrotask(() => request.dispatchEvent(new Event('success')))
  return request
})
beforeEach(() => {
  blobs.clear()
  open.mockClear()
  vi.stubGlobal('indexedDB', { open })
  signInHistoryOwner(101)
})
afterEach(() => {
  vi.unstubAllGlobals()
  useAuthStore.getState().auth.reset()
})

test('same logical blob key never crosses account boundaries and unowned legacy blobs remain quarantined', async () => {
  const legacy = new Blob(['unowned'])
  blobs.set('same-key', legacy)
  expect(await loadImageBlob('same-key')).toBeNull()
  const a = new Blob(['private A'])
  const b = new Blob(['private B'])
  await saveImageBlob('same-key', a)
  signInHistoryOwner(202)
  expect(await loadImageBlob('same-key')).toBeNull()
  await saveImageBlob('same-key', b)
  expect(await loadImageBlob('same-key')).toBe(b)
  await deleteImageBlob('same-key')
  signInHistoryOwner(101)
  expect(await loadImageBlob('same-key')).toBe(a)
  expect(blobs.get('same-key')).toBe(legacy)
})

test('unknown/anonymous auth performs no IndexedDB reads or writes', async () => {
  useAuthStore.getState().auth.reset()
  expect(await loadImageBlob('same-key')).toBeNull()
  await deleteImageBlob('same-key')
  await expect(saveImageBlob('same-key', new Blob(['x']))).rejects.toThrow(
    'authenticated owner'
  )
  expect(open).not.toHaveBeenCalled()
})

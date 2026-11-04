/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { beforeEach, afterEach, expect, test, vi } from 'vitest'

import {
  useWalletTransferStore,
  walletTransferVersion,
  WalletTransferChanged,
} from '../wallet-transfer-store'

beforeEach(() => {
  localStorage.clear()
  let tail = Promise.resolve<unknown>(undefined)
  vi.stubGlobal('navigator', {
    locks: {
      request: (_name: string, criticalSection: () => unknown) => {
        const result = tail.then(criticalSection)
        tail = result.catch(() => undefined)
        return result
      },
    },
  })
})
afterEach(() => {
  localStorage.clear()
  vi.unstubAllGlobals()
})
const store = () => useWalletTransferStore.getState()

test('two stale blank pages cannot silently submit different amounts under one identity', async () => {
  const first = await store().begin(42, null, 500_000, '1')
  await expect(store().begin(42, null, 1_000_000, '2')).rejects.toBeInstanceOf(
    WalletTransferChanged
  )
  expect(store().read(42)).toEqual(first)
})
test('a stale retry after another page completes must not generate another debit identity', async () => {
  const first = await store().begin(42, null, 500_000, '1')
  await store().finish(42, first.request_id, 'completed')
  await expect(
    store().begin(42, walletTransferVersion(first), 500_000, '1')
  ).rejects.toBeInstanceOf(WalletTransferChanged)
})
test('a blank page opened before a completed transfer cannot create a second transfer', async () => {
  const first = await store().begin(42, null, 500_000, '1')
  await store().finish(42, first.request_id, 'completed')
  await expect(store().begin(42, null, 500_000, '1')).rejects.toBeInstanceOf(
    WalletTransferChanged
  )
})
test.each(['completed', 'rejected'] as const)(
  'late %s for an older request cannot erase a newer intent',
  async (status) => {
    const first = await store().begin(42, null, 500_000, '1')
    await store().finish(42, first.request_id, 'completed')
    const second = await store().begin(
      42,
      walletTransferVersion(store().read(42)),
      1_000_000,
      '2'
    )
    expect(await store().finish(42, first.request_id, status)).toBe(false)
    expect(store().read(42)).toEqual(second)
  }
)
test('refresh uses the same pending request and immutable quota', async () => {
  const first = await store().begin(42, null, 500_000, '1')
  const loaded = store().read(42)
  expect(
    await store().begin(42, walletTransferVersion(loaded), 9_000_000, '18')
  ).toEqual(first)
})
test('missing Web Locks fails closed rather than allowing unsafe cross-tab writes', async () => {
  vi.stubGlobal('navigator', {})
  await expect(store().begin(42, null, 500_000, '1')).rejects.toThrow(
    'This browser cannot safely coordinate wallet transfers.'
  )
  expect(store().read(42)).toBeNull()
})
test('concurrent blank pages serialize the creation of one pending operation', async () => {
  const results = await Promise.allSettled([
    store().begin(42, null, 500_000, '1'),
    store().begin(42, null, 1_000_000, '2'),
  ])
  expect(results.filter((r) => r.status === 'fulfilled')).toHaveLength(1)
  expect(results.filter((r) => r.status === 'rejected')).toHaveLength(1)
})

test('confirmed rejection permits a corrected new intent only from the latest revision', async () => {
  const first = await store().begin(42, null, 500_000, '1')
  await store().finish(42, first.request_id, 'rejected')
  const second = await store().begin(
    42,
    walletTransferVersion(store().read(42)),
    250_000,
    '0.5'
  )
  expect(second.request_id).not.toBe(first.request_id)
  expect(second.quota).toBe(250_000)
  expect(second.status).toBe('pending')
})

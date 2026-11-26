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
  transferConfirmationDeadline,
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
const owner = 42
const store = () => useWalletTransferStore.getState()
const lost = new Error('Network unavailable')
const refused = new Error('Insufficient funds for this attempt')
const execute = (
  version: string | null,
  quota: number,
  dispatch: Parameters<ReturnType<typeof store>['execute']>[0]['dispatch']
) =>
  store().execute({
    userId: owner,
    version,
    draft: { quota, amount: String(quota) },
    current: () => true,
    dispatch,
    isDefiniteRejection: (error) => error === refused,
  })
const record = () => store().read(owner)

test('a second tab cannot dispatch while the first tab still holds the lock', async () => {
  let rejectFirst!: (error: unknown) => void
  let acquired!: () => void
  const firstStarted = new Promise<void>((resolve) => {
    acquired = resolve
  })
  const first = execute(
    null,
    50,
    () =>
      new Promise((_, reject) => {
        rejectFirst = reject
        acquired()
      })
  )
  await firstStarted
  const prior = record()
  const stale = vi.fn(async () => {})
  const review = execute(null, 100, stale)
  const replay = vi.fn(async () => {
    throw refused
  })
  const retry = execute(walletTransferVersion(prior), 50, replay)
  await Promise.resolve()
  expect(stale).not.toHaveBeenCalled()
  expect(replay).not.toHaveBeenCalled()
  const failed = expect(first).rejects.toThrow(lost.message)
  const unchanged = expect(review).rejects.toBeInstanceOf(WalletTransferChanged)
  const rejected = expect(retry).rejects.toThrow(refused.message)
  rejectFirst(lost)
  await Promise.all([failed, unchanged, rejected])
  expect(stale).not.toHaveBeenCalled()
  expect(replay).toHaveBeenCalledWith(prior)
  expect(record()).toEqual(prior)
  await expect(
    execute(walletTransferVersion(prior), 100, stale)
  ).rejects.toBeInstanceOf(WalletTransferChanged)
  expect(stale).not.toHaveBeenCalled()
  await execute(
    walletTransferVersion(prior),
    50,
    vi.fn(async () => {})
  )
  expect(record()).toMatchObject({
    request_id: prior?.request_id,
    status: 'completed',
  })
})

test('a confirmation deadline releases the lock without allowing a late result to change the pending receipt', async () => {
  vi.useFakeTimers()
  try {
    let resolveLate!: () => void
    const first = execute(
      null,
      50,
      () =>
        new Promise((resolve) => {
          resolveLate = resolve
        })
    )
    const ambiguous = expect(first).rejects.toThrow(/awaiting confirmation/)
    await vi.advanceTimersByTimeAsync(transferConfirmationDeadline)
    await ambiguous
    const prior = record()
    await expect(
      execute(walletTransferVersion(prior), 50, async () => {
        throw refused
      })
    ).rejects.toBe(refused)
    resolveLate()
    await Promise.resolve()
    expect(record()).toEqual(prior)
    expect(record()?.status).toBe('pending')
  } finally {
    vi.useRealTimers()
  }
})

test('a stale blank page or stale retry after another page has settled cannot create another identity', async () => {
  await expect(
    execute(null, 50, async () => {
      throw lost
    })
  ).rejects.toBe(lost)
  const version = walletTransferVersion(record())
  await execute(version, 50, async () => {})
  const unwanted = vi.fn(async () => {})
  await expect(execute(null, 50, unwanted)).rejects.toBeInstanceOf(
    WalletTransferChanged
  )
  await expect(execute(version, 50, unwanted)).rejects.toBeInstanceOf(
    WalletTransferChanged
  )
  expect(unwanted).not.toHaveBeenCalled()
})

test('missing Web Locks fails closed rather than allowing unsafe cross-tab writes', async () => {
  vi.stubGlobal('navigator', {})
  const dispatch = vi.fn(async () => {})
  await expect(execute(null, 50, dispatch)).rejects.toThrow(
    'This browser cannot safely coordinate wallet transfers.'
  )
  expect(dispatch).not.toHaveBeenCalled()
  expect(record()).toBeNull()
})

test('a definite initial rejection permits a corrected new intent only after review', async () => {
  await expect(
    execute(null, 50, async () => {
      throw refused
    })
  ).rejects.toBe(refused)
  const first = record()
  expect(first?.status).toBe('rejected')
  await execute(walletTransferVersion(first), 25, async () => {})
  expect(record()?.request_id).not.toBe(first?.request_id)
  expect(record()?.quota).toBe(25)
})

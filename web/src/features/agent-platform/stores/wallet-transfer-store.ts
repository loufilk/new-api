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
import { z } from 'zod'
import { create } from 'zustand'

const intentSchema = z.object({
  request_id: z.string().uuid(),
  quota: z.number().int().positive().max(Number.MAX_SAFE_INTEGER),
  amount: z.string().max(24),
  target: z.enum(['wallet', 'agent']).optional(),
  status: z.enum(['pending', 'completed', 'rejected']),
})
export type WalletTransferIntent = z.infer<typeof intentSchema>
export const walletTransferVersion = (
  intent: WalletTransferIntent | null
): string | null => (intent ? `${intent.request_id}:${intent.status}` : null)
export class WalletTransferChanged extends Error {
  constructor(public readonly current: WalletTransferIntent | null) {
    super('The transfer changed in another page. Review it before continuing.')
  }
}
type TransferDraft = Pick<WalletTransferIntent, 'quota' | 'amount' | 'target'>

interface TransferExecution {
  userId: number
  version: string | null
  draft: TransferDraft
  current: () => boolean
  dispatch: (intent: WalletTransferIntent) => Promise<void>
  isDefiniteRejection: (error: unknown) => boolean
}

export const transferConfirmationDeadline = 30_000

async function withConfirmationDeadline(work: () => Promise<void>) {
  let timer: ReturnType<typeof setTimeout> | undefined
  try {
    const deadline = new Promise<never>((_, reject) => {
      timer = setTimeout(
        () =>
          reject(
            new Error(
              'A transfer is awaiting confirmation. Retry the same transfer to check its result. Closing this dialog does not cancel a submitted transfer.'
            )
          ),
        transferConfirmationDeadline
      )
    })
    await Promise.race([deadline, work()])
  } finally {
    clearTimeout(timer)
  }
}

export function createWalletTransferStore(namespace: string) {
  const key = (userId: number) => `${namespace}:${userId}`
  return create<{
    read: (userId: number) => WalletTransferIntent | null
    execute: (operation: TransferExecution) => Promise<boolean>
  }>((_, get) => ({
    read: (userId) => {
      const saved = localStorage.getItem(key(userId))
      return saved ? intentSchema.parse(JSON.parse(saved)) : null
    },
    execute: async ({
      userId,
      version,
      draft,
      current,
      dispatch,
      isDefiniteRejection,
    }) => {
      if (!navigator.locks?.request) {
        throw new Error(
          'This browser cannot safely coordinate wallet transfers.'
        )
      }
      return navigator.locks.request(key(userId), async () => {
        if (!current()) return false
        const saved = get().read(userId)
        if (walletTransferVersion(saved) !== version) {
          throw new WalletTransferChanged(saved)
        }
        const prior = saved?.status === 'pending' ? saved : null
        // An existing pending entry is indistinguishable from an admitted
        // request after response loss or browser crash, including older entries.
        const intent =
          prior ??
          intentSchema.parse({
            request_id: crypto.randomUUID(),
            ...draft,
            status: 'pending',
          })
        if (intent.quota !== draft.quota || intent.target !== draft.target) {
          throw new WalletTransferChanged(intent)
        }
        if (!prior) localStorage.setItem(key(userId), JSON.stringify(intent))
        const finish = (status: 'completed' | 'rejected') => {
          const latest = get().read(userId)
          if (walletTransferVersion(latest) !== walletTransferVersion(intent)) {
            throw new WalletTransferChanged(latest)
          }
          // A tombstone prevents stale pages from creating another debit.
          localStorage.setItem(
            key(userId),
            JSON.stringify({ ...intent, status })
          )
        }
        try {
          await withConfirmationDeadline(() => dispatch(intent))
        } catch (error) {
          // A non-durable rejection says only that THIS attempt did not debit.
          // An earlier dispatch could still be settling; do not release its ID.
          if (!prior && isDefiniteRejection(error)) finish('rejected')
          throw error
        }
        finish('completed')
        return true
      })
    },
  }))
}

export const useWalletTransferStore = createWalletTransferStore(
  'agent-wallet-transfer'
)

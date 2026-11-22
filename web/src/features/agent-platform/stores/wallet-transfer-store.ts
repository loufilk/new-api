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
const key = (userId: number) => `agent-wallet-transfer:${userId}`
export const useWalletTransferStore = create<{
  read: (userId: number) => WalletTransferIntent | null
  begin: (
    userId: number,
    version: string | null,
    quota: number,
    amount: string
  ) => Promise<WalletTransferIntent>
  finish: (
    userId: number,
    requestId: string,
    status: 'completed' | 'rejected'
  ) => Promise<boolean>
}>((_, get) => ({
  read: (userId) => {
    const saved = localStorage.getItem(key(userId))
    return saved ? intentSchema.parse(JSON.parse(saved)) : null
  },
  begin: async (userId, version, quota, amount) => {
    if (!navigator.locks?.request) {
      throw new Error('This browser cannot safely coordinate wallet transfers.')
    }
    return navigator.locks.request(key(userId), () => {
      const saved = get().read(userId)
      if (walletTransferVersion(saved) !== version) {
        throw new WalletTransferChanged(saved)
      }
      if (saved?.status === 'pending') return saved
      const intent = intentSchema.parse({
        request_id: crypto.randomUUID(),
        quota,
        amount,
        status: 'pending',
      })
      localStorage.setItem(key(userId), JSON.stringify(intent))
      return intent
    })
  },
  finish: async (userId, requestId, status) => {
    if (!navigator.locks?.request) {
      throw new Error('This browser cannot safely coordinate wallet transfers.')
    }
    return navigator.locks.request(key(userId), () => {
      const saved = get().read(userId)
      if (saved?.request_id !== requestId || saved.status !== 'pending') {
        return false
      }
      // A tombstone prevents stale blank pages from creating another debit.
      localStorage.setItem(key(userId), JSON.stringify({ ...saved, status }))
      return true
    })
  },
}))

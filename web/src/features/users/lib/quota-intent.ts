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
import { z } from 'zod'

import type { QuotaAdjustMode } from '../types'

const identity = {
  actorId: z.number().int().positive(),
  userId: z.number().int().positive(),
  request_id: z.uuid(),
}
const schema = z.discriminatedUnion('status', [
  z.object({
    ...identity,
    status: z.literal('pending'),
    mode: z.enum(['add', 'subtract', 'override']),
    value: z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER),
  }),
  z.object({ ...identity, status: z.literal('closed') }),
])

export type QuotaIntent = z.infer<typeof schema>
export type PendingQuotaIntent = Extract<QuotaIntent, { status: 'pending' }>
export type Scope = { actorId: number; userId: number }

export function quotaIntentKey(scope: Scope) {
  if (
    ![scope.actorId, scope.userId].every(
      (id) => Number.isSafeInteger(id) && id > 0
    )
  ) {
    throw new Error('Invalid quota adjustment identity')
  }
  return `newapi:admin-quota:v1:${scope.actorId}:${scope.userId}`
}

export function readQuotaIntent(scope: Scope): QuotaIntent | null {
  const raw = localStorage.getItem(quotaIntentKey(scope))
  if (raw === null) return null
  const record = schema.parse(JSON.parse(raw))
  if (
    record.actorId !== scope.actorId ||
    record.userId !== scope.userId ||
    (record.status === 'pending' && record.mode !== 'override' && !record.value)
  ) {
    throw new Error('Invalid quota adjustment record')
  }
  return record
}

function write(scope: Scope, record: QuotaIntent) {
  localStorage.setItem(quotaIntentKey(scope), JSON.stringify(record))
}

// Call inside the actor/target Web Lock. Never turn a stale tab's click into a
// new adjustment, even if another tab has already completed the previous one.
export function beginQuotaIntent(
  scope: Scope,
  displayed: QuotaIntent | null,
  mode: QuotaAdjustMode,
  value: number
): PendingQuotaIntent | null {
  const current = readQuotaIntent(scope)
  if (
    current?.request_id !== displayed?.request_id ||
    current?.status !== displayed?.status
  ) {
    return null
  }
  if (current?.status === 'pending') {
    if (
      displayed?.status !== 'pending' ||
      current.mode !== displayed.mode ||
      current.value !== displayed.value
    ) {
      return null
    }
    return current
  }
  const record: PendingQuotaIntent = {
    ...scope,
    status: 'pending',
    request_id: crypto.randomUUID(),
    mode,
    value,
  }
  write(scope, record)
  return record
}

// Keep only a version marker for stale tabs; do not retain the amount or mode.
export function finishQuotaIntent(record: PendingQuotaIntent) {
  const { actorId, userId, request_id } = record
  write(record, { actorId, userId, request_id, status: 'closed' })
}

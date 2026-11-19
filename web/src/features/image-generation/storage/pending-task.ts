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

import { getImageHistoryOwnerId } from './history-owner'

const pendingTask = z.object({
  id: z.string().regex(/^[0-9a-f]{48}$/),
  expiresAt: z.number().finite(),
  prompt: z.string(),
  model: z.string(),
  size: z.string(),
  count: z.number().int().min(1).max(4),
  resolution: z.string().optional(),
  quality: z.string(),
  style: z.string(),
})
export type PendingImageTask = z.infer<typeof pendingTask>

// Do not persist API keys, uploaded files, response bytes or arbitrary extra fields.
export function readPendingImageTask(
  owner = getImageHistoryOwnerId()
): PendingImageTask | null {
  if (!owner || owner !== getImageHistoryOwnerId()) return null
  try {
    const prefix = `image-pending:v1:${owner}:`
    for (let index = localStorage.length - 1; index >= 0; index--) {
      const key = localStorage.key(index)
      if (!key?.startsWith(prefix)) continue
      let record: unknown
      try {
        record = JSON.parse(localStorage.getItem(key) ?? 'null')
      } catch {
        record = null
      }
      const item = pendingTask.safeParse(record)
      if (
        item.success &&
        key === prefix + item.data.id &&
        item.data.expiresAt > Date.now()
      ) {
        return item.data
      }
      localStorage.removeItem(key)
    }
  } catch {
    /* Storage is optional. */
  }
  return null
}
export function writePendingImageTask(
  owner: number | null,
  id: string,
  task: PendingImageTask | null
) {
  if (
    !owner ||
    owner !== getImageHistoryOwnerId() ||
    !/^[0-9a-f]{48}$/.test(id)
  ) {
    return
  }
  try {
    const key = `image-pending:v1:${owner}:${id}`
    if (task) localStorage.setItem(key, JSON.stringify(pendingTask.parse(task)))
    else localStorage.removeItem(key)
  } catch {
    /* Keep the live page functional when browser storage is unavailable. */
  }
}

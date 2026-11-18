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

const announcementSchema = z.object({
  id: z.union([z.number(), z.string()]).optional(),
  title: z.string().optional(),
  content: z.string().min(1),
  publishDate: z.string(),
  type: z.string().optional(),
  extra: z.string().optional(),
  category: z.enum(['announcement', 'timeline']).optional(),
  pinned: z.boolean().optional(),
  status: z.enum(['draft', 'published']).optional(),
})

export type Announcement = z.infer<typeof announcementSchema>

export function publishedAnnouncementItems(
  value: unknown,
  now = Date.now()
): Announcement[] {
  if (!Array.isArray(value)) return []
  const items: Announcement[] = []
  for (const candidate of value) {
    const result = announcementSchema.safeParse(candidate)
    if (!result.success) continue
    const item = result.data
    const publishedAt = Date.parse(item.publishDate)
    if (
      item.status === 'draft' ||
      !Number.isFinite(publishedAt) ||
      publishedAt > now
    ) {
      continue
    }
    items.push(item)
  }
  return items.sort(
    (left, right) =>
      Number(right.pinned === true) - Number(left.pinned === true) ||
      Date.parse(right.publishDate) - Date.parse(left.publishDate)
  )
}

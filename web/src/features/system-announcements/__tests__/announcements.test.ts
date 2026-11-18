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
import { expect, test } from 'vitest'

import { publishedAnnouncementItems } from '../announcements'

const now = Date.parse('2026-11-18T12:00:00Z')
const legacy = { id: 1, content: 'Legacy', publishDate: '2026-11-17T12:00:00Z' }

test('drafts and scheduled entries stay hidden while legacy publications remain available', () => {
  const entries = [
    legacy,
    { ...legacy, id: 2, status: 'draft' },
    { ...legacy, id: 3, publishDate: '2026-11-19T00:00:00Z' },
    { ...legacy, id: 4, publishDate: 'invalid' },
  ]
  expect(
    publishedAnnouncementItems(entries, now).map((item) => item.id)
  ).toEqual([1])
})

test('pinned updates appear first without mutating the saved collection', () => {
  const entries = [
    legacy,
    { ...legacy, id: 2, pinned: true, publishDate: '2026-11-16T00:00:00Z' },
    { ...legacy, id: 3, publishDate: '2026-11-18T00:00:00Z' },
  ]
  expect(
    publishedAnnouncementItems(entries, now).map((item) => item.id)
  ).toEqual([2, 3, 1])
  expect(entries.map((item) => item.id)).toEqual([1, 2, 3])
})

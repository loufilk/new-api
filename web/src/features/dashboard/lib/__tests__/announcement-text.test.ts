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

import { getPreviewText } from '../text'

test.each([
  [
    'Before ![Schedule](https://images.example.test/a(long).png) after',
    'Before Schedule after',
  ],
  [
    '![Schedule][img]\n\nText follows.\n\n[img]: https://images.example.test/a.png',
    'Schedule Text follows.',
  ],
  [
    '[Read details](https://example.test/long/path) and **text**',
    'Read details and text',
  ],
  ['# Title\n\nOld <b>announcement</b>', 'Title Old announcement'],
])(
  'announcement summary keeps readable content without Markdown destinations: %s',
  (source, expected) => {
    expect(getPreviewText(source, 200)).toBe(expected)
  }
)

test('long announcement summaries remain bounded', () => {
  expect(getPreviewText('a'.repeat(10000))).toBe(`${'a'.repeat(60)}...`)
})

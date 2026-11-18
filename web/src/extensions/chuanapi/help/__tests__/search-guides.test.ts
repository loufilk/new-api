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
import { describe, expect, it } from 'vitest'

import { orderedGuides } from '../content/nav'
import { searchGuides } from '../lib/search-guides'

describe('guide search', () => {
  it('finds a client by slug and case-insensitive text', () => {
    expect(
      searchGuides(orderedGuides, 'CODEX CLI').some(
        (guide) => guide.slug === 'codex-cli'
      )
    ).toBe(true)
  })
  it('searches troubleshooting content and requires every term', () => {
    expect(searchGuides(orderedGuides, '401').length).toBeGreaterThan(0)
    expect(
      searchGuides(orderedGuides, 'codex missing-unfindable-term')
    ).toEqual([])
  })
  it('returns the full ordered guide list when the query is empty', () => {
    expect(searchGuides(orderedGuides, '  ')).toEqual(orderedGuides)
  })
})

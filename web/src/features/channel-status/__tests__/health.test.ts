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

import { filterHealthGroups, groupHealth } from '../health'

const base = {
  name: 'Alpha',
  description: 'Fast model group',
  ratio: 0.4,
  available: true,
  success_rate: 96,
  history: [],
}

test('zero success, absent samples and unavailable routes remain distinct states', () => {
  expect(groupHealth({ ...base, success_rate: 0 })).toBe('degraded')
  expect(groupHealth({ ...base, success_rate: null })).toBe('unknown')
  expect(groupHealth({ ...base, available: false })).toBe('unavailable')
  expect(groupHealth(base)).toBe('healthy')
})

test('search matches descriptions and composes with the selected health filter', () => {
  const groups = [
    base,
    { ...base, name: 'Beta', description: 'Cheap', success_rate: null },
  ]
  expect(filterHealthGroups(groups, 'fast', 'healthy')).toEqual([base])
  expect(filterHealthGroups(groups, 'fast', 'unknown')).toEqual([])
  expect(
    filterHealthGroups(groups, '', 'unknown').map((group) => group.name)
  ).toEqual(['Beta'])
})

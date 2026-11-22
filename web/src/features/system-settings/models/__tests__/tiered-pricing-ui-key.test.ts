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

import { carryStableUiKey, getStableUiKey } from '../tiered-pricing-ui-key'

describe('tiered pricing UI keys', () => {
  it('keeps a stable key for the same object without adding data fields', () => {
    const tier = { label: 'base', conditions: [] }

    const first = getStableUiKey(tier)
    const second = getStableUiKey(tier)

    expect(second).toBe(first)
    expect(Object.keys(tier)).toEqual(['label', 'conditions'])
    expect(JSON.stringify(tier)).not.toContain(first)
  })

  it('uses distinct keys for distinct business objects', () => {
    const first = { value: 1 }
    const second = { value: 1 }

    expect(getStableUiKey(second)).not.toBe(getStableUiKey(first))
  })

  it('carries identity to an immutable replacement', () => {
    const condition = { value: 1000 }
    const replacement = { ...condition, value: 2000 }
    const key = getStableUiKey(condition)

    expect(carryStableUiKey(condition, replacement)).toBe(replacement)
    expect(getStableUiKey(replacement)).toBe(key)
    expect(Object.keys(replacement)).toEqual(['value'])
    expect(JSON.stringify(replacement)).toBe('{"value":2000}')
  })
})

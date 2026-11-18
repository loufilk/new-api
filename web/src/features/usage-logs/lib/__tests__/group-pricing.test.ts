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

import { resolveLogGroupPricing } from '../group-pricing'

describe('recorded group pricing', () => {
  it.each([0.4, 0])(
    'uses personal absolute multiplier %s before the account-class multiplier',
    (ratio) => {
      expect(
        resolveLogGroupPricing({
          group_ratio: ratio,
          user_group_ratio: 0.6,
          user_group_price_override: ratio,
          inherited_group_ratio: 0.6,
          group_ratio_source: 'user',
        })
      ).toEqual({ ratio, source: 'user' })
    }
  )

  it('keeps the account-class multiplier for historical logs without personal pricing', () => {
    expect(
      resolveLogGroupPricing({ group_ratio: 1, user_group_ratio: 0.6 })
    ).toEqual({ ratio: 0.6, source: 'account_group' })
  })

  it('uses the group multiplier when the historical account-class sentinel is present', () => {
    expect(
      resolveLogGroupPricing({ group_ratio: 0.8, user_group_ratio: -1 })
    ).toEqual({ ratio: 0.8, source: 'global' })
  })

  it('preserves an explicitly free historical account-class price', () => {
    expect(
      resolveLogGroupPricing({ group_ratio: 1, user_group_ratio: 0 })
    ).toEqual({ ratio: 0, source: 'account_group' })
  })

  it('returns no price when the log has no recorded multiplier', () => {
    expect(resolveLogGroupPricing(null)).toEqual({
      ratio: null,
      source: 'global',
    })
  })

  it('rejects a nonfinite personal multiplier instead of displaying it as a price', () => {
    expect(
      resolveLogGroupPricing({
        user_group_price_override: Number.NaN,
        group_ratio: 0.8,
      })
    ).toEqual({ ratio: 0.8, source: 'global' })
  })
})

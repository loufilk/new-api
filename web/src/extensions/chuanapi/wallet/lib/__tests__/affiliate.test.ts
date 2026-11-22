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
import { describe, expect, test } from 'vitest'

import { formatReferralRate, generateAffiliateLink } from '../affiliate'

describe('formatReferralRate', () => {
  test('renders whole ratios with one decimal from the configured rule', () => {
    expect(formatReferralRate(0.05)).toBe('5.0%')
    expect(formatReferralRate(0.2)).toBe('20.0%')
  })

  test('renders fractional ratios without trailing zeros', () => {
    expect(formatReferralRate(0.0333)).toBe('3.33%')
  })

  test('renders zero or invalid ratios as 0%', () => {
    expect(formatReferralRate(0)).toBe('0%')
    expect(formatReferralRate(Number.NaN)).toBe('0%')
    expect(formatReferralRate(-0.1)).toBe('0%')
  })
})

describe('generateAffiliateLink', () => {
  test('uses the current origin with the sign-up path', () => {
    expect(generateAffiliateLink('abcd')).toBe(
      `${window.location.origin}/sign-up?aff=abcd`
    )
  })
})

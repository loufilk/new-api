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
import { expect, it } from 'vitest'

import { groupPricingFormSchema } from '../lib/schema'

it.each(['', ' ', '-0.1', 'Infinity', 'NaN', '1e999'])(
  'rejects invalid personal multiplier %j',
  (ratio) => {
    expect(
      groupPricingFormSchema.safeParse({ rules: [{ group: 'premium', ratio }] })
        .success
    ).toBe(false)
  }
)

it.each(['0', '0.00001', '0.4', '1.2'])(
  'accepts finite non-negative multiplier %s without a one-times cap',
  (ratio) => {
    expect(
      groupPricingFormSchema.parse({ rules: [{ group: 'premium', ratio }] })
        .rules[0].ratio
    ).toBe(ratio)
  }
)

it('rejects Auto and duplicate billing groups because each rule targets one actual group', () => {
  expect(
    groupPricingFormSchema.safeParse({
      rules: [{ group: 'auto', ratio: '0.4' }],
    }).success
  ).toBe(false)
  expect(
    groupPricingFormSchema.safeParse({
      rules: [
        { group: 'premium', ratio: '0.4' },
        { group: 'premium', ratio: '0.5' },
      ],
    }).success
  ).toBe(false)
})

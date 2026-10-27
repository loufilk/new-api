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

import { ledgerReasonLabelKey } from '../ledger'

describe('ledgerReasonLabelKey', () => {
  test('maps known backend reasons to i18n keys', () => {
    expect(ledgerReasonLabelKey('signup_bonus')).toBe('Signup Bonus')
    expect(ledgerReasonLabelKey('billing_pre_consume')).toBe(
      'Billing Pre-consumed'
    )
    expect(ledgerReasonLabelKey('referral_transfer')).toBe(
      'Referral Reward Transfer'
    )
  })

  test('passes through unknown reasons for forward compatibility', () => {
    expect(ledgerReasonLabelKey('future_reason')).toBe('future_reason')
    expect(ledgerReasonLabelKey('')).toBe('')
  })
})

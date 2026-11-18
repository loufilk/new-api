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

import { formatUSDT, quotaToUSDT, usdtToQuota } from '../amount'
import { withdrawalConfigSchema } from '../schema'

describe('withdrawal exact amount conversion', () => {
  test('preserves six decimals and rejects unrepresentable or unsafe amounts', () => {
    expect(usdtToQuota('1.234566', 500000)).toBe(617283)
    expect(usdtToQuota('0.000001', 500000)).toBeNull()
    expect(usdtToQuota('0.000002', 500000)).toBe(1)
    for (const amount of [
      '-1',
      '0',
      'NaN',
      '1e9',
      '0.1234567',
      '999999999999',
    ]) {
      expect(usdtToQuota(amount, 1000000000000)).toBeNull()
    }
    expect(quotaToUSDT(617283, 500000)).toBe('1.234566')
    expect(formatUSDT('9007199254740991')).toBe('9007199254.740991')
    expect(formatUSDT('-1')).toBe('-0.000001')
  })

  test('rejects policy values that permit a zero payout or unsafe quota arithmetic', () => {
    const config = {
      revision: 0,
      enabled: false,
      min_consumed_quota: 0,
      min_withdrawal_quota: 500000,
      quota_per_usdt: 500000,
      bsc_fee_bps: 0,
      polygon_fee_bps: 0,
      trc20_fee_bps: 0,
      instructions: '',
      updated_at: 0,
      updated_by: 0,
    }
    expect(withdrawalConfigSchema.safeParse(config).success).toBe(true)
    expect(
      withdrawalConfigSchema.safeParse({ ...config, quota_per_usdt: 0 }).success
    ).toBe(false)
    expect(
      withdrawalConfigSchema.safeParse({ ...config, bsc_fee_bps: 10000 })
        .success
    ).toBe(false)
    expect(
      withdrawalConfigSchema.safeParse({
        ...config,
        min_withdrawal_quota: Number.MAX_SAFE_INTEGER + 1,
      }).success
    ).toBe(false)
  })
})

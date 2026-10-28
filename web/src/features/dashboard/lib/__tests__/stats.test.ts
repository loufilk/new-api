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

import { calculateDashboardStats } from '../stats'

describe('dashboard cache usage totals', () => {
  test('adds cache categories independently without changing total tokens', () => {
    const rows = [
      {
        created_at: 3600,
        quota: 12,
        count: 1,
        token_used: 21,
        cache_read_tokens: 8,
        cache_creation_tokens: 4,
      },
      {
        created_at: 3600,
        quota: 3,
        count: 2,
        token_used: 9,
        cache_read_tokens: 2,
        cache_creation_tokens: 6,
      },
    ]
    expect(calculateDashboardStats(rows)).toEqual({
      totalQuota: 15,
      totalCount: 3,
      totalTokens: 30,
      totalCacheReadTokens: 10,
      totalCacheCreationTokens: 10,
    })
  })

  test('returns zero cache totals for empty and historical rows', () => {
    expect(calculateDashboardStats([])).toMatchObject({
      totalCacheReadTokens: 0,
      totalCacheCreationTokens: 0,
    })
    expect(
      calculateDashboardStats([{ created_at: 3600, token_used: 17 }])
    ).toMatchObject({
      totalTokens: 17,
      totalCacheReadTokens: 0,
      totalCacheCreationTokens: 0,
    })
  })

  test('ignores negative and nonfinite cache counts', () => {
    const rows = [
      {
        created_at: 3600,
        cache_read_tokens: -2,
        cache_creation_tokens: Infinity,
      },
      {
        created_at: 3600,
        cache_read_tokens: Number.NaN,
        cache_creation_tokens: -7,
      },
      { created_at: 3600, cache_read_tokens: 5, cache_creation_tokens: 3 },
    ]
    expect(calculateDashboardStats(rows)).toMatchObject({
      totalCacheReadTokens: 5,
      totalCacheCreationTokens: 3,
    })
  })
})

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
import { describe, expect, test, vi } from 'vitest'

vi.mock('@/lib/currency', () => ({
  getCurrencyDisplay: () => ({
    config: { quotaPerUnit: 500000 },
    meta: { kind: 'usd', symbol: '$', exchangeRate: 1 },
  }),
}))

vi.mock('@/lib/time', () => ({
  formatChartTime: (timestamp: number) => `t${timestamp}`,
}))

const { processChartData, processUserChartData } = await import('../charts')

function quotaItem(overrides = {}) {
  return {
    created_at: 1700000000,
    model_name: 'gpt-4',
    quota: 1000,
    count: 2,
    token_used: 50,
    username: 'alice',
    ...overrides,
  }
}

describe('dashboard chart aggregation boundaries', () => {
  test('aggregates quota and count without throwing on fresh time buckets', () => {
    const result = processChartData([quotaItem(), quotaItem()], 'day')

    expect(result.totalCountDisplay).toContain('4')
    expect(result.totalQuotaDisplay.includes('$')).toBe(true)
  })

  test('returns empty specs for empty input', () => {
    const result = processChartData([], 'day')

    expect(result.totalCountDisplay).toContain('0')
  })

  test('aggregates per-user trend without throwing on fresh time buckets', () => {
    const result = processUserChartData(
      [quotaItem(), quotaItem({ username: 'bob', quota: 500 })],
      'day',
      undefined,
      10
    )

    const rankValues = (result.spec_user_rank.data[0] as { values: unknown[] })
      .values
    expect(rankValues.length).toBe(2)
  })
})

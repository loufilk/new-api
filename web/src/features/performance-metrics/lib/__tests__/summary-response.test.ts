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

import {
  InvalidPerformanceSummaryError,
  selectDashboardPerformanceSummary,
} from '../summary-response'

const summary = { success_rate: 99, avg_latency_ms: 120, avg_tps: 4 }
const model = { ...summary, model_name: 'test-model' }

describe('dashboard performance response validation', () => {
  it.each([undefined, null])(
    'accepts absent or empty historical aggregate %j without inventing a result',
    (aggregate) => {
      const response = {
        success: true,
        data: { models: [], summary: aggregate },
      }
      const data = selectDashboardPerformanceSummary(response)
      expect(data.models).toEqual([])
      expect(data.summary).toBe(aggregate)
    }
  )

  it('keeps valid weighted metrics without mutating the raw response', () => {
    const response = {
      success: true,
      data: {
        summary,
        models: [model],
        recent_success_series: { private: [] },
      },
      extra: 'preserved',
    }
    const before = structuredClone(response)
    expect(selectDashboardPerformanceSummary(response)).toEqual({
      summary,
      models: [model],
    })
    expect(response).toEqual(before)
  })

  it.each([
    ['missing success', { data: { models: [] } }],
    ['null data', { success: true, data: null }],
    ['object models', { success: true, data: { models: {} } }],
    ['null model', { success: true, data: { models: [null] } }],
    [
      'string metric',
      { success: true, data: { models: [{ ...model, avg_tps: '4' }] } },
    ],
    [
      'nonfinite metric',
      { success: true, data: { models: [{ ...model, avg_tps: Infinity }] } },
    ],
    [
      'negative latency',
      { success: true, data: { models: [{ ...model, avg_latency_ms: -1 }] } },
    ],
    [
      'out-of-range rate',
      { success: true, data: { models: [{ ...model, success_rate: 101 }] } },
    ],
    [
      'object name',
      { success: true, data: { models: [{ ...model, model_name: {} }] } },
    ],
    [
      'invalid aggregate',
      { success: true, data: { models: [model], summary: 'private payload' } },
    ],
  ])(
    'rejects %s with a stable error rather than coercing to zero',
    (_case, response) => {
      expect(() => selectDashboardPerformanceSummary(response)).toThrow(
        InvalidPerformanceSummaryError
      )
    }
  )

  it('does not retain or expose malformed response values in validation errors', () => {
    let failure: unknown
    try {
      selectDashboardPerformanceSummary({
        success: true,
        data: { models: 'secret synthetic payload' },
      })
    } catch (error) {
      failure = error
    }
    expect(failure).toBeInstanceOf(InvalidPerformanceSummaryError)
    expect(failure).toMatchObject({ code: 'INVALID_PERFORMANCE_SUMMARY' })
    expect(String(failure)).not.toContain('secret')
    expect((failure as Error).cause).toBeUndefined()
  })

  it('preserves the business rejection cause rather than misclassifying it as empty data', () => {
    const response = { success: false, message: 'Rejected' }
    try {
      selectDashboardPerformanceSummary(response)
      expect.fail('Unsuccessful response should reject')
    } catch (error) {
      expect(error).not.toBeInstanceOf(InvalidPerformanceSummaryError)
      expect((error as Error).cause).toBe(response)
    }
  })
})

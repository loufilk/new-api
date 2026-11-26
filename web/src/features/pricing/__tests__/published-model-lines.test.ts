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
import { mergePublishedModelLines } from '../lib/published-model-lines'
import { getRecentGroupActivity, marketRateDifference } from '../lib/group-market-activity'
import type { PricingData, PublishedModelLinesResponse } from '../types'

const first = 'line-00000000000000000000000000000001'
const second = 'line-00000000000000000000000000000002'
const base: PricingData = {
  success: true,
  data: [{
    id: 1, model_name: 'same-model', quota_type: 0, model_ratio: 1,
    completion_ratio: 1, enable_groups: ['rate-1'],
    supported_endpoint_types_by_group: { 'rate-1': ['openai', 'gemini'] },
  }],
  group_ratio: { 'rate-1': 1 },
  usable_group: { 'rate-1': { desc: 'Rate ×1', ratio: 1 } },
  supported_endpoint: {},
  vendors: [],
  auto_groups: [],
}
const named = {
  success: true,
  public_lines: [
    { id: first, label: 'First', ratio: 1, is_new: true },
    { id: second, label: 'Second', ratio: 1, previous_ratio: 0.7 },
  ],
  models: [{
    model_name: 'same-model', public_line_ids: [first, second],
    supported_endpoint_types_by_public_line: { [first]: ['openai'], [second]: ['gemini'] },
  }],
} satisfies PublishedModelLinesResponse

describe('revocable public lines', () => {
  it('separates same-rate lines and keeps exact per-line protocols and anonymous tiers', () => {
    const view = mergePublishedModelLines(base, named)
    expect(view.data[0].enable_groups).toEqual([first, second, 'rate-1'])
    expect(view.data[0].supported_endpoint_types_by_group?.[first]).toEqual(['openai'])
    expect(view.data[0].supported_endpoint_types_by_group?.[second]).toEqual(['gemini'])
    expect(view.group_catalog?.[second].label).toBe('Second')
    expect(view.group_ratio?.[second]).toBe(1)
    const activity = getRecentGroupActivity(undefined, named.public_lines[1])
    expect(activity?.change && marketRateDifference(activity.change)).toBe('+0.3')
    expect(mergePublishedModelLines(base)).toBe(base)
    expect(base.data[0].enable_groups).toEqual(['rate-1'])
  })
  it('forgets a withdrawn line even with a warm anonymous base', () => {
    const view = mergePublishedModelLines(base, { ...named, public_lines: named.public_lines.slice(1) })
    expect(view.data[0].enable_groups).toEqual([second, 'rate-1'])
    expect(view.group_catalog?.[first]).toBeUndefined()
    expect(view.data[0].supported_endpoint_types_by_group?.[first]).toBeUndefined()
  })
})

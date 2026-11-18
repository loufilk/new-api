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
import { expect, test } from 'vitest'

import {
  filterQualityTargets,
  summarizeQualityTargets,
} from '../target-summary'
import type { TestRun } from '../types'

function run(model: string, created_at: number, reduced = false): TestRun {
  return {
    id: `${model}-${created_at}`,
    model,
    group: 'group',
    created_at,
    completed_at: created_at,
    status: 'completed',
    assessment: '',
    benchmark_id: 'local-fingerprint-v1',
    max_output_tokens: 0,
    estimated_quota: null,
    request_id: '',
    error_code: '',
    cancel_requested: false,
    fingerprint_result: {
      status: reduced ? 'not_model' : 'scored',
      reason: '',
      top_candidates: reduced
        ? []
        : [
            {
              model,
              display_name: model,
              family: 'gpt',
              family_name: 'GPT',
              score: 1,
              probability: null,
            },
          ],
      sample_quality: [
        {
          challenge_id: 'a',
          expected_count: 1,
          parsed_numbers: 1,
          minimum_numbers: 1,
          accepted: true,
          truncated: false,
          out_of_range_count: 0,
          reason: '',
        },
      ],
      data_version: '',
      data_sha256: '',
      sdk_commit: '',
      probability_note: '',
      probability_status: '',
      used_outputs: 1,
      strict: true,
    },
  }
}

test('summaries use the latest valid result without adding retired targets', () => {
  const targets = [
    { group: 'group', model: 'current' },
    { group: 'group', model: 'empty' },
  ]
  const summary = summarizeQualityTargets(targets, [
    run('current', 1),
    run('current', 2, true),
    run('retired', 3),
  ])
  expect(summary).toEqual([
    { group: 'group', model: 'current', capability: 'reduced' },
    { group: 'group', model: 'empty', capability: null },
  ])
  expect(
    filterQualityTargets(summary, 'group', 'unknown').map((item) => item.model)
  ).toEqual(['empty'])
  expect(
    filterQualityTargets(summary, 'current', 'reduced').map(
      (item) => item.model
    )
  ).toEqual(['current'])
})

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
  validateModelTestTargets,
  type ModelTestSettingsInput,
} from '../model-test-settings'

const catalogue = {
  groups: ['default'],
  models_by_group: { default: ['gpt-test'] },
  routes: [{ id: 1, name: 'Primary', group: 'default', model: 'gpt-test' }],
}
function settings(): ModelTestSettingsInput {
  return {
    enabled: true,
    version: 0,
    automatic_enabled: true,
    default_period_secs: 3600,
    daily_budget_quota: 1000,
    targets: [
      {
        id: '9ab0a56f-f0bf-46de-afae-e4874c62ed03',
        group: 'default',
        model: 'gpt-test',
        channel_id: 1,
        enabled: true,
        period_secs: 0,
        max_output_tokens: 2048,
        max_run_quota: 100,
      },
    ],
  }
}

test('valid one-hour target with inherited interval passes validation', () => {
  expect(validateModelTestTargets(settings(), catalogue)).toBe(true)
})
test.each([0, 59, 604801, Number.NaN])('default period %s blocks saving', (period) => {
  const input = settings()
  input.default_period_secs = period
  expect(validateModelTestTargets(input, catalogue)).toBe(false)
})
test.each([0, -1, Number.MAX_SAFE_INTEGER + 1, Number.NaN])(
  'invalid daily quota %s blocks automatic tests',
  (quota) => {
    const input = settings()
    input.daily_budget_quota = quota
    expect(validateModelTestTargets(input, catalogue)).toBe(false)
  }
)
test('duplicate group and model targets cannot be saved even on different channels', () => {
  const input = settings()
  input.targets.push({
    ...input.targets[0],
    id: 'cbe8493b-af4e-4a6b-804c-fb979471fc21',
    channel_id: 2,
  })
  expect(
    validateModelTestTargets(input, {
      ...catalogue,
      routes: [...catalogue.routes, { ...catalogue.routes[0], id: 2 }],
    })
  ).toBe(false)
})
test('per-run quota exceeding daily budget blocks automatic tests', () => {
  const input = settings()
  input.targets[0].max_run_quota = 1001
  expect(validateModelTestTargets(input, catalogue)).toBe(false)
})
test('catalogue route mismatch fails closed', () => {
  const input = settings()
  input.targets[0].channel_id = 2
  expect(validateModelTestTargets(input, catalogue)).toBe(false)
})

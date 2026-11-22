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
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  getModelTestSettings,
  saveModelTestSettings,
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
        channel_id: 0,
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
test.each([0, 59, 604801, Number.NaN])(
  'default period %s blocks saving',
  (period) => {
    const input = settings()
    input.default_period_secs = period
    expect(validateModelTestTargets(input, catalogue)).toBe(false)
  }
)
test('automatic tests accept no daily budget or output token limit', () => {
  const input = settings()
  input.daily_budget_quota = 0
  input.targets[0].max_output_tokens = 0
  input.targets[0].max_run_quota = 0
  expect(validateModelTestTargets(input, catalogue)).toBe(true)
})
test('automatic tests still require an enabled target', () => {
  const input = settings()
  input.targets[0].enabled = false
  expect(validateModelTestTargets(input, catalogue)).toBe(false)
})
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
test('legacy quota fields do not block automatic tests', () => {
  const input = settings()
  input.targets[0].max_run_quota = 1001
  expect(validateModelTestTargets(input, catalogue)).toBe(true)
})
test('valid group and model require no channel routes', () => {
  expect(
    validateModelTestTargets(settings(), { ...catalogue, routes: [] })
  ).toBe(true)
})
test('legacy pinned targets remain editable even when their channel is absent', () => {
  const input = settings()
  input.targets[0].channel_id = 99
  expect(validateModelTestTargets(input, { ...catalogue, routes: [] })).toBe(
    true
  )
})
test('model outside the selected group blocks saving', () => {
  const input = settings()
  input.targets[0].model = 'unavailable-model'
  expect(validateModelTestTargets(input, catalogue)).toBe(false)
})

test.each(['target', 'all tests'])(
  'unavailable targets can be disabled through %s but cannot be enabled again',
  (scope) => {
    const input = settings()
    input.targets[0].group = 'retired-group'
    input.targets[0].model = 'retired-model'
    input.automatic_enabled = false
    if (scope === 'target') input.targets[0].enabled = false
    else input.enabled = false
    expect(validateModelTestTargets(input, catalogue)).toBe(true)
    input.enabled = true
    input.targets[0].enabled = true
    expect(validateModelTestTargets(input, catalogue)).toBe(false)
  }
)

test('disabled unavailable targets still require valid fields, periods and unique selections', () => {
  const input = settings()
  input.enabled = false
  input.automatic_enabled = false
  input.targets[0].model = 'retired-model'
  expect(validateModelTestTargets(input, catalogue)).toBe(true)
  input.targets[0].period_secs = 59
  expect(validateModelTestTargets(input, catalogue)).toBe(false)
  input.targets[0].period_secs = 0
  input.targets[0].model = ''
  expect(validateModelTestTargets(input, catalogue)).toBe(false)
  input.targets[0].model = 'retired-model'
  input.targets.push({
    ...input.targets[0],
    id: 'cbe8493b-af4e-4a6b-804c-fb979471fc21',
  })
  expect(validateModelTestTargets(input, catalogue)).toBe(false)
})

afterEach(() => vi.restoreAllMocks())

test('settings load accepts a group and model catalogue without route details', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...settings(),
        engine_ready: true,
        catalogue: {
          groups: ['default'],
          models_by_group: { default: ['gpt-test'] },
        },
      },
    },
  })
  const loaded = await getModelTestSettings()
  expect(loaded.catalogue.routes).toEqual([])
  expect(validateModelTestTargets(loaded, loaded.catalogue)).toBe(true)
})

test('saving legacy settings clears channel and budget limits without mutating the input', async () => {
  const input = settings()
  input.targets[0].channel_id = 99
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  await saveModelTestSettings(input)
  expect(put).toHaveBeenCalledWith(
    '/api/model-tests/settings',
    expect.objectContaining({
      daily_budget_quota: 0,
      targets: [
        expect.objectContaining({
          group: 'default',
          model: 'gpt-test',
          channel_id: 0,
          max_run_quota: 0,
          max_output_tokens: 0,
        }),
      ],
    })
  )
  expect(input.targets[0].channel_id).toBe(99)
})

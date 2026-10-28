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

import { channelSchema } from '../../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  buildSettingJSON,
  transformChannelToFormDefaults,
} from '../channel-form'

test.each(['x'.repeat(65), 'line\nname', 'a\u202eb', 'a\u200bb'])(
  'invalid public owner %s is rejected before a channel save',
  (owner) => {
    const result = channelFormSchema.safeParse({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'internal',
      models: 'model',
      group: ['default'],
      key: 'test-only',
      model_owned_by: owner,
    })
    expect(result.success).toBe(false)
    if (!result.success) {
      expect(
        result.error.issues.some((issue) => issue.path[0] === 'model_owned_by')
      ).toBe(true)
    }
  }
)

test('public owner round trips and uses Unicode characters for the length limit', () => {
  const settings = {
    ...CHANNEL_FORM_DEFAULT_VALUES,
    name: 'internal',
    models: 'model',
    group: ['default'],
    key: 'test-only',
    model_owned_by: '川'.repeat(64),
  }
  expect(channelFormSchema.safeParse(settings).success).toBe(true)
  expect(JSON.parse(buildSettingJSON(settings)).model_owned_by).toBe(
    '川'.repeat(64)
  )
  const channel = channelSchema.parse({
    id: 1,
    name: 'internal',
    key: '',
    type: 1,
    status: 1,
    created_time: 0,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    models: 'model',
    group: 'default',
    setting: buildSettingJSON(settings),
  })
  expect(transformChannelToFormDefaults(channel).model_owned_by).toBe(
    '川'.repeat(64)
  )
  expect(
    JSON.parse(buildSettingJSON({ ...settings, model_owned_by: '' }))
  ).not.toHaveProperty('model_owned_by')
})

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
import type { TFunction } from 'i18next'
import { expect, test } from 'vitest'

import {
  getApiKeyFormDefaultValues,
  getApiKeyFormSchema,
  transformFormDataToPayload,
} from '../api-key-form'

const t = ((key: string) => key) as TFunction

test('a new key starts without an implicit manual or Auto group', () => {
  expect(getApiKeyFormDefaultValues().group).toBe('')
})

test.each([false, true])(
  'a new key with smart routing %s still requires an explicit group',
  (smartRouting) => {
    const result = getApiKeyFormSchema(t).safeParse({
      ...getApiKeyFormDefaultValues(),
      name: 'new key',
      smart_routing: smartRouting,
      group: '',
    })
    expect(result.success).toBe(false)
    if (result.success) return
    expect(result.error.issues).toContainEqual(
      expect.objectContaining({
        path: ['group'],
        message: 'Please select a group',
      })
    )
  }
)

test('whitespace is rejected without silently changing a group name', () => {
  const values = { ...getApiKeyFormDefaultValues(), name: 'new key' }
  expect(
    getApiKeyFormSchema(t).safeParse({ ...values, group: '   ' }).success
  ).toBe(false)
  expect(
    transformFormDataToPayload({ ...values, group: ' default ' }).group
  ).toBe(' default ')
})

test('the concrete default group remains valid when the user selects it', () => {
  const values = {
    ...getApiKeyFormDefaultValues(),
    name: 'explicit default',
    group: 'default',
  }
  expect(getApiKeyFormSchema(t).safeParse(values).success).toBe(true)
  expect(transformFormDataToPayload(values).group).toBe('default')
})

test('only the legacy edit context can retain an inherited empty group', () => {
  const values = { ...getApiKeyFormDefaultValues(), name: 'legacy key' }
  expect(getApiKeyFormSchema(t, 5, true).safeParse(values).success).toBe(true)
  expect(getApiKeyFormSchema(t, 5, false).safeParse(values).success).toBe(false)
})

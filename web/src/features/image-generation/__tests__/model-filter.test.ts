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

import { getAvailableImageModels, getEnabledApiKeys } from '../lib/model-filter'
import type { ImageApiKeyOption } from '../types'

function makeToken(
  overrides: Partial<ImageApiKeyOption> = {}
): ImageApiKeyOption {
  return {
    id: 7,
    name: 'key-7',
    group: 'default',
    model_limits_enabled: false,
    model_limits: '',
    status: 1,
    ...overrides,
  }
}

const pricing = [
  { model_name: 'dall-e-3', supported_endpoint_types: ['image-generation'] },
  { model_name: 'gpt-image-2', supported_endpoint_types: ['image-generation'] },
  { model_name: 'gpt-4o', supported_endpoint_types: ['chat'] },
]

describe('getAvailableImageModels intersects image capabilities with the server token scope', () => {
  test.each(['', ' , '])(
    'an enabled empty allowlist %j permits no models',
    (model_limits) => {
      expect(
        getAvailableImageModels({
          pricingModels: pricing,
          tokenModels: [],
          token: makeToken({ model_limits_enabled: true, model_limits }),
        })
      ).toEqual([])
    }
  )

  test('keeps only image endpoint models present in the server token scope', () => {
    const models = getAvailableImageModels({
      pricingModels: pricing,
      tokenModels: ['dall-e-3', 'gpt-4o'],
      token: makeToken(),
    })
    expect(models).toEqual(['dall-e-3'])
  })

  test('preserves server-authorized models without duplicating whitelist rules', () => {
    const models = getAvailableImageModels({
      pricingModels: pricing,
      tokenModels: ['gpt-image-2'],
      token: makeToken({
        model_limits_enabled: true,
        model_limits: 'image-provider-alias',
      }),
    })
    expect(models).toEqual(['gpt-image-2'])
  })

  test('returns empty when the token is missing or the token has no image capability overlap', () => {
    expect(
      getAvailableImageModels({
        pricingModels: pricing,
        tokenModels: ['dall-e-3'],
        token: null,
      })
    ).toEqual([])
    expect(
      getAvailableImageModels({
        pricingModels: pricing,
        tokenModels: ['gpt-4o'],
        token: makeToken(),
      })
    ).toEqual([])
  })
})

describe('getEnabledApiKeys keeps only status-enabled tokens', () => {
  test('filters out disabled, expired, and exhausted tokens', () => {
    const keys = getEnabledApiKeys([
      { status: 1 },
      { status: 2 },
      { status: 3 },
      { status: 4 },
    ])
    expect(keys).toEqual([{ status: 1 }])
  })
})

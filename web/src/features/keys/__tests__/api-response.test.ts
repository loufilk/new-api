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
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  getApiKey,
  getApiKeys,
  searchApiKeys,
  createApiKey,
  updateApiKey,
} from '../api'
import {
  getApiKeyFormDefaultValues,
  transformApiKeyToFormDefaults,
  transformFormDataToPayload,
} from '../lib/api-key-form'
import { apiKeySchema } from '../types'

const legacyKey = {
  id: 7,
  name: 'legacy fixture',
  key: 'masked********fixture',
  status: 1,
  remain_quota: 120,
  used_quota: 80,
  unlimited_quota: false,
  expired_time: -1,
  created_time: 10,
  accessed_time: 20,
  smart_routing: false,
  routing_strategy: '',
  no_retry_within_group: false,
  project_label: '',
  group: 'restricted',
  auto_groups: null,
  cross_group_retry: false,
  model_limits_enabled: true,
  model_limits: 'allowed-model',
  allow_ips: '192.0.2.1',
  allow_user_quota_query: false,
}

afterEach(() => vi.restoreAllMocks())

it('keeps API key search credentials in the request body instead of the URL', async () => {
  const response = {
    data: { success: true, data: { items: [legacyKey], total: 1 } },
  }
  const get = vi.spyOn(api, 'get').mockResolvedValue(response)
  const post = vi.spyOn(api, 'post').mockResolvedValue(response)

  const result = await searchApiKeys({
    keyword: 'production',
    token: 'sk-synthetic-search-secret',
    p: 2,
    size: 20,
  })

  expect(post).toHaveBeenCalledWith('/api/token/search?p=2&size=20', {
    keyword: 'production',
    token: 'sk-synthetic-search-secret',
  })
  expect(get).not.toHaveBeenCalled()
  expect(result.data?.items[0]?.id).toBe(legacyKey.id)
})

describe('legacy key routing response compatibility', () => {
  it('accepts the backend empty strategy as auto without changing any access or quota setting', () => {
    const parsed = apiKeySchema.parse(legacyKey)
    expect(parsed).toEqual({ ...legacyKey, routing_strategy: 'auto' })
    const payload = transformFormDataToPayload(
      transformApiKeyToFormDefaults(parsed)
    )
    expect(payload).toMatchObject({
      routing_strategy: 'auto',
      smart_routing: false,
      no_retry_within_group: false,
      group: 'restricted',
      model_limits_enabled: true,
      model_limits: 'allowed-model',
      allow_ips: '192.0.2.1',
      allow_user_quota_query: false,
      unlimited_quota: false,
      remain_quota: 120,
    })
  })

  it('keeps omitted legacy fields compatible and all four explicit strategies unchanged', () => {
    const { routing_strategy: _omitted, ...oldKey } = legacyKey
    expect(
      transformApiKeyToFormDefaults(apiKeySchema.parse(oldKey)).routing_strategy
    ).toBe('auto')
    for (const strategy of ['auto', 'success_rate', 'price', 'speed']) {
      expect(
        apiKeySchema.parse({ ...legacyKey, routing_strategy: strategy })
          .routing_strategy
      ).toBe(strategy)
    }
  })

  it.each(['unknown', 'AUTO', ' ', null, 0, false])(
    'rejects invalid routing strategy %s instead of coercing it to auto',
    (strategy) => {
      expect(
        apiKeySchema.safeParse({ ...legacyKey, routing_strategy: strategy })
          .success
      ).toBe(false)
    }
  )

  it.each([
    'smart_routing',
    'no_retry_within_group',
    'unlimited_quota',
    'model_limits_enabled',
    'allow_user_quota_query',
  ])(
    'does not coerce an invalid %s flag while normalizing the strategy',
    (field) => {
      expect(
        apiKeySchema.safeParse({ ...legacyKey, [field]: 'false' }).success
      ).toBe(false)
    }
  )

  it.each(['list', 'search'] as const)(
    'validates and normalizes a mixed legacy/new %s response before rendering',
    async (endpoint) => {
      vi.spyOn(api, endpoint === 'search' ? 'post' : 'get').mockResolvedValue({
        data: {
          success: true,
          data: {
            items: [
              legacyKey,
              {
                ...legacyKey,
                id: 8,
                smart_routing: true,
                routing_strategy: 'speed',
              },
            ],
            total: 2,
          },
        },
      })
      const response =
        endpoint === 'list'
          ? await getApiKeys()
          : await searchApiKeys({ keyword: 'fixture' })
      expect(response.data?.items.map((item) => item.routing_strategy)).toEqual(
        ['auto', 'speed']
      )
      expect(response.data?.total).toBe(2)
    }
  )

  it('validates the detail response before loading the edit form', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: legacyKey },
    })
    expect((await getApiKey(7)).data?.routing_strategy).toBe('auto')
  })

  it.each(['list', 'search', 'detail'] as const)(
    'rejects malformed %s data without exposing the row or silently dropping it',
    async (endpoint) => {
      const invalid = {
        ...legacyKey,
        routing_strategy: 'invalid-private-input',
      }
      vi.spyOn(api, endpoint === 'search' ? 'post' : 'get').mockResolvedValue({
        data: {
          success: true,
          data:
            endpoint === 'detail'
              ? invalid
              : { items: [legacyKey, invalid], total: 2 },
        },
      })
      let request
      if (endpoint === 'detail') request = getApiKey(7)
      else if (endpoint === 'list') request = getApiKeys()
      else request = searchApiKeys({ keyword: 'fixture' })
      await expect(request).rejects.toThrow('Failed to load API keys')
    }
  )

  it('keeps failed server responses and mutation acknowledgements unchanged', async () => {
    const failed = { success: false, message: 'Forbidden', data: undefined }
    vi.spyOn(api, 'get').mockResolvedValue({ data: failed })
    expect(await getApiKeys()).toEqual(failed)
    expect(await getApiKey(7)).toEqual(failed)
    const ack = { success: true, message: '' }
    vi.spyOn(api, 'post').mockResolvedValue({ data: ack })
    vi.spyOn(api, 'put').mockResolvedValue({ data: ack })
    const payload = transformFormDataToPayload({
      ...getApiKeyFormDefaultValues(),
      name: 'new fixture',
    })
    expect(await createApiKey(payload)).toEqual(ack)
    expect(await updateApiKey({ ...payload, id: 7 })).toEqual(ack)
  })
})

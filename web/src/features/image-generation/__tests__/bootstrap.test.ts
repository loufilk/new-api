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

import { getApiKeys } from '@/features/keys/api'
import type { ApiKey, GetApiKeysResponse } from '@/features/keys/types'
import { getPricing } from '@/features/pricing/api'
import type { PricingData } from '@/features/pricing/types'

import { loadImagePageBootstrap } from '../api'

vi.mock('@/features/keys/api', () => ({
  fetchTokenKey: vi.fn(),
  getApiKeys: vi.fn(),
}))

vi.mock('@/features/pricing/api', () => ({
  getPricing: vi.fn(),
}))

const mockedGetApiKeys = vi.mocked(getApiKeys)
const mockedGetPricing = vi.mocked(getPricing)

function makeKey(id: number, status = 1, group = 'default'): ApiKey {
  return {
    id,
    name: `key-${id}`,
    key: 'sk-test',
    status,
    remain_quota: 0,
    used_quota: 0,
    unlimited_quota: true,
    expired_time: -1,
    created_time: 0,
    accessed_time: 0,
    group,
    auto_groups: null,
    cross_group_retry: false,
    model_limits_enabled: false,
    model_limits: '',
    allow_ips: '',
    allow_user_quota_query: true,
  }
}

function makeKeysPage(
  items: ApiKey[],
  total: number,
  pageSize: number,
  page = 1
): GetApiKeysResponse {
  return {
    success: true,
    data: { items, total, page, page_size: pageSize },
  }
}

function makePricing(): PricingData {
  return {
    success: true,
    data: [
      {
        id: 1,
        model_name: 'dall-e-3',
        quota_type: 0,
        model_ratio: 1,
        completion_ratio: 1,
        enable_groups: ['default'],
        supported_endpoint_types: ['image-generation'],
      },
    ],
    vendors: [],
    group_ratio: {},
    usable_group: {},
    supported_endpoint: {},
    auto_groups: [],
  }
}

describe('loadImagePageBootstrap aggregates every key page', () => {
  test('keeps only enabled keys when everything fits on the first page', async () => {
    mockedGetApiKeys.mockResolvedValue(
      makeKeysPage([makeKey(1), makeKey(2, 2), makeKey(3, 4)], 3, 100)
    )
    mockedGetPricing.mockResolvedValue(makePricing())

    const bootstrap = await loadImagePageBootstrap()

    expect(mockedGetApiKeys).toHaveBeenCalledTimes(1)
    expect(mockedGetApiKeys).toHaveBeenCalledWith({ p: 1, size: 100 })
    expect(bootstrap.keys.map((key) => key.id)).toEqual([1])
    expect(bootstrap.pricingModels.map((model) => model.model_name)).toEqual([
      'dall-e-3',
    ])
  })

  test('loads remaining pages in parallel and merges only enabled keys', async () => {
    mockedGetApiKeys.mockImplementation(async (params) => {
      if (params?.p === 1) {
        return makeKeysPage([makeKey(1), makeKey(2, 2)], 5, 2, 1)
      }
      if (params?.p === 2) {
        return makeKeysPage([makeKey(3), makeKey(4, 3)], 5, 2, 2)
      }
      return makeKeysPage([makeKey(5)], 5, 2, 3)
    })
    mockedGetPricing.mockResolvedValue(makePricing())

    const bootstrap = await loadImagePageBootstrap()

    expect(mockedGetApiKeys).toHaveBeenCalledTimes(3)
    expect(mockedGetApiKeys).toHaveBeenCalledWith({ p: 1, size: 100 })
    expect(mockedGetApiKeys).toHaveBeenCalledWith({ p: 2, size: 2 })
    expect(mockedGetApiKeys).toHaveBeenCalledWith({ p: 3, size: 2 })
    expect(bootstrap.keys.map((key) => key.id)).toEqual([1, 3, 5])
  })

  test('treats a missing page body as an empty single page', async () => {
    mockedGetApiKeys.mockResolvedValue({ success: true })
    mockedGetPricing.mockResolvedValue(makePricing())

    const bootstrap = await loadImagePageBootstrap()

    expect(mockedGetApiKeys).toHaveBeenCalledTimes(1)
    expect(bootstrap.keys).toEqual([])
  })
})

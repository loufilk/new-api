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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { ModelDetailsContent } from '../components/model-details'
import { getDisplayPriceContext } from '../lib/model-helpers'
import type { PricingModel } from '../types'

vi.mock('@visactor/react-vchart', () => ({ VChart: () => null }))

let client: QueryClient

beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { groups: [] } },
  })
})

afterEach(() => client.clear())

describe('personal free group pricing', () => {
  it('identifies the same cheapest group used in the model card price and preserves an explicit group', () => {
    const model: PricingModel = {
      id: 1,
      model_name: 'example-model',
      quota_type: 0,
      model_ratio: 1,
      completion_ratio: 3,
      enable_groups: ['default', 'personal-free'],
      group_ratio: { default: 1, 'personal-free': 0 },
    }
    expect(getDisplayPriceContext(model)).toEqual({
      group: 'personal-free',
      ratio: 0,
      isStartingPrice: true,
    })
    expect(getDisplayPriceContext(model, 'default')).toEqual({
      group: 'default',
      ratio: 1,
      isStartingPrice: false,
    })
  })
  it.each([
    { billing_mode: 'ratio' },
    {
      billing_mode: 'tiered_expr',
      billing_expr: 'tier("standard", p * 2 + c * 8)',
    },
    {
      billing_mode: 'tiered_expr',
      billing_expr: 'tier("video", u("seconds") * 0.2)',
      billing_usage_schema: { seconds: { type: 'number', unit: 'second' } },
    },
  ] satisfies Partial<PricingModel>[])(
    'preserves zero in the visible group multiplier for $billing_mode',
    (pricing) => {
      const model: PricingModel = {
        id: 1,
        model_name: 'example-model',
        quota_type: 0,
        model_ratio: 1,
        completion_ratio: 3,
        enable_groups: ['personal-free'],
        group_ratio: { 'personal-free': 0 },
        ...pricing,
      }
      render(
        <QueryClientProvider client={client}>
          <ModelDetailsContent
            model={model}
            groupRatio={{ 'personal-free': 0 }}
            usableGroup={{
              'personal-free': { desc: 'Personal price', ratio: 0 },
            }}
            endpointMap={{}}
            autoGroups={[]}
            priceRate={1}
            usdExchangeRate={1}
            tokenUnit='M'
          />
        </QueryClientProvider>
      )
      expect(screen.getByText('0x')).toBeVisible()
      expect(screen.queryByText('1x')).not.toBeInTheDocument()
    }
  )
})

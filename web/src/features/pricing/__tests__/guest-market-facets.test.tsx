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
import { render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { PricingSidebar } from '../components/pricing-sidebar'
import { filterAndSortModels } from '../lib/filters'
import {
  marketFacets,
  publicTierCatalog,
  publicModelBrand,
} from '../lib/market-facets'
import {
  getModelEndpointTypes,
  getModelEndpoints,
} from '../lib/model-endpoints'
import type { PricingModel } from '../types'

const fixtures: Pick<
  PricingModel,
  | 'id'
  | 'model_name'
  | 'vendor_id'
  | 'vendor_name'
  | 'enable_groups'
  | 'supported_endpoint_types_by_group'
>[] = [
  {
    id: 1,
    model_name: 'a',
    vendor_id: 1,
    vendor_name: 'Google',
    enable_groups: ['rate-1', 'rate-2'],
    supported_endpoint_types_by_group: { 'rate-1': ['gemini'], 'rate-2': [] },
  },
  {
    id: 2,
    model_name: 'b',
    vendor_id: 2,
    vendor_name: 'Anthropic',
    enable_groups: ['rate-1'],
    supported_endpoint_types_by_group: { 'rate-1': ['anthropic'] },
  },
  {
    id: 3,
    model_name: 'gpt-imposter',
    vendor_id: 0,
    vendor_name: undefined,
    enable_groups: ['rate-9'],
    supported_endpoint_types_by_group: { 'rate-9': [] },
  },
  {
    id: 4,
    model_name: 'd',
    vendor_id: 11,
    vendor_name: 'Gemini',
    enable_groups: ['rate-2'],
    supported_endpoint_types_by_group: { 'rate-2': [] },
  },
]
const rows: PricingModel[] = fixtures.map((x) => ({
  ...x,
  group_keys_are_display_only: true,
  supported_endpoint_types: ['gemini', 'anthropic', 'openai-response-compact'],
  quota_type: 0,
  model_ratio: 1,
  completion_ratio: 1,
}))
const ratios = { 'rate-1': 1, 'rate-2': 2, 'rate-9': 9 }
const groups = Object.keys(ratios)
const catalog = publicTierCatalog(rows, ratios)
describe('guest model-market facets', () => {
  it('groups the published vendor directory without attributing an entire mixed tier to one vendor', () => {
    const scoped = marketFacets(rows, groups, catalog, 'gemini', true)
    expect(scoped.options.map((o) => [o.key, o.count])).toEqual([
      ['anthropic', 1],
      ['gemini', 2],
      ['other', 1],
    ])
    expect(scoped.visibleGroups).toEqual(['rate-1', 'rate-2'])
    expect(scoped.visibleModels.map((row) => row.model_name)).toEqual([
      'a',
      'd',
    ])
    expect(scoped.priceGroups).toBeUndefined()
    expect(catalog['rate-1']).toMatchObject({
      label: '×1',
      public_tier_icon: undefined,
    })
    expect(catalog['rate-2']).toMatchObject({
      label: '×2',
      public_tier_icon: 'Gemini.Color',
    })
    expect(catalog['rate-9'].public_tier_icon).toBeUndefined()
  })
  it('merges known vendor aliases, keeps a custom published vendor, never guesses from a model ID', () => {
    for (const [a, b] of [
      ['阿里巴巴', 'Alibaba'],
      ['MiniMax', 'MiniMax (minimax.io)'],
      ['Moonshot', 'Moonshot AI'],
      ['智谱', 'Z.AI'],
    ]) {
      expect(publicModelBrand({ vendor_name: a }).key).toBe(
        publicModelBrand({ vendor_name: b }).key
      )
    }
    expect(publicModelBrand({ vendor_id: 20, vendor_name: 'Acme' })).toEqual({
      key: 'vendor-20',
      label: 'Acme',
      icon: null,
    })
    expect(publicModelBrand(rows[2]).key).toBe('other')
  })
  it('uses only group-confirmed protocols, including for guest compatibility with a legacy cached response', () => {
    const options = {
      group: 'all',
      search: '',
      sortBy: 'name',
      quotaType: 'all',
      endpointType: 'all',
    }
    const { visibleModels } = marketFacets(rows, groups, catalog, 'all', true)
    expect(filterAndSortModels(visibleModels, options)).toHaveLength(4)
    expect(
      filterAndSortModels(visibleModels, { ...options, endpointType: 'gemini' })
    ).toEqual([rows[0]])
    expect(
      filterAndSortModels(visibleModels, {
        ...options,
        group: 'rate-2',
        endpointType: 'gemini',
      })
    ).toEqual([])
    expect(
      filterAndSortModels(visibleModels, {
        ...options,
        endpointType: 'openai-response-compact',
      })
    ).toEqual([])
    const old = { ...rows[0], supported_endpoint_types_by_group: undefined }
    expect(getModelEndpointTypes(old)).toEqual([])
    expect(getModelEndpointTypes(old, 'rate-1')).toEqual([])
    expect(
      getModelEndpointTypes({
        ...rows[0],
        supported_endpoint_types_by_group: {
          'not-in-public-response': ['anthropic'],
        },
      })
    ).toEqual([])
    expect(
      getModelEndpoints(
        { ...old, supported_endpoint_types_by_group: { 'rate-1': ['openai'] } },
        { openai: { path: '/v1/chat/completions' } },
        'rate-1'
      )
    ).toHaveLength(1)
  })
  it('renders model counts and neutral mixed tiers; tier and vendor both constrain endpoint counts', () => {
    const props = {
      models: rows,
      groups,
      groupCatalog: catalog,
      groupRatios: ratios,
      displayTiers: true,
      platform: 'gemini',
      groupFilter: 'rate-2',
      quotaTypeFilter: 'all',
      endpointTypeFilter: 'all',
      onPlatformChange: vi.fn(),
      onGroupChange: vi.fn(),
      onQuotaTypeChange: vi.fn(),
      onEndpointTypeChange: vi.fn(),
    }
    const { rerender } = render(<PricingSidebar {...props} />)
    expect(
      within(screen.getByRole('group', { name: 'Platform' })).getByRole(
        'button',
        { name: 'Gemini 2' }
      )
    ).toBeVisible()
    const tiers = within(screen.getByRole('group', { name: 'Pricing tiers' }))
    expect(tiers.getByRole('button', { name: '×2' })).toHaveAttribute(
      'aria-pressed',
      'true'
    )
    expect(tiers.queryByText('×9')).toBeNull()
    const protocols = within(
      screen.getByRole('group', { name: 'Endpoint Type' })
    )
    expect(protocols.getByRole('button', { name: 'All 2' })).toBeVisible()
    expect(protocols.getByRole('button', { name: 'Gemini 0' })).toBeVisible()
    rerender(<PricingSidebar {...props} groupFilter='all' />)
    expect(protocols.getByRole('button', { name: 'Gemini 1' })).toBeVisible()
    expect(protocols.getByRole('button', { name: 'Anthropic 0' })).toBeVisible()
  })
  it('does not confuse an authenticated group platform with a public model vendor', () => {
    const m = {
      ...rows[0],
      group_keys_are_display_only: false,
      enable_groups: ['A', 'B'],
      supported_endpoint_types_by_group: { A: ['anthropic'], B: ['openai'] },
    }
    const facet = marketFacets(
      [m],
      ['A', 'B'],
      { A: { platform: 'openai' }, B: { platform: 'anthropic' } },
      'openai',
      false
    )
    expect(facet.visibleModels[0].enable_groups).toEqual(['A', 'B'])
    expect(getModelEndpointTypes(facet.visibleModels[0])).toEqual(['anthropic'])
    expect(m.supported_endpoint_types_by_group).toEqual({
      A: ['anthropic'],
      B: ['openai'],
    })
  })
})

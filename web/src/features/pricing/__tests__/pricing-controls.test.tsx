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
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { PricingSidebar } from '../components/pricing-sidebar'
import {
  PricingToolbar,
  type PricingToolbarProps,
} from '../components/pricing-toolbar'
import { SearchBar } from '../components/search-bar'
import { filterByQuotaType } from '../lib/filters'
import type { PricingModel } from '../types'

function toolbarProps(): PricingToolbarProps {
  return {
    filteredCount: 2,
    totalCount: 2,
    sortBy: 'name',
    tokenUnit: 'M',
    showRechargePrice: false,
    viewMode: 'card',
    onSortChange: vi.fn(),
    onTokenUnitChange: vi.fn(),
    onRechargePriceChange: vi.fn(),
    onViewModeChange: vi.fn(),
  }
}

function filterProps() {
  return {
    platform: 'all',
    groupFilter: 'all',
    quotaTypeFilter: 'all',
    endpointTypeFilter: 'all',
    onPlatformChange: vi.fn(),
    onGroupChange: vi.fn(),
    onQuotaTypeChange: vi.fn(),
    onEndpointTypeChange: vi.fn(),
    groups: ['basic', 'premium', 'vip'],
    groupRatios: { basic: 1, premium: 2, vip: 0 },
    groupCatalog: {
      basic: { platform: 'openai' },
      premium: { platform: 'anthropic' },
      vip: {
        platform: 'openai',
        exclusive: true,
        label: 'Private tier',
        badges: ['benefit'],
      },
    },
    models: [],
  }
}
describe('pricing controls', () => {
  it.each<{
    name: string
    pricing: Partial<PricingModel>
    matches: string[]
  }>([
    { name: 'legacy token pricing', pricing: {}, matches: ['token'] },
    {
      name: 'legacy per-request pricing',
      pricing: { quota_type: 1, model_price: 0.01 },
      matches: ['request'],
    },
    {
      name: 'dynamic fixed pricing with a legacy token quota type',
      pricing: {
        billing_mode: 'tiered_expr',
        billing_expr: 'tier("request", fixed(0.01))',
      },
      matches: ['request'],
    },
    {
      name: 'free dynamic per-request pricing',
      pricing: {
        billing_mode: 'tiered_expr',
        billing_expr: 'v1:tier("free", fixed(0))',
      },
      matches: ['request'],
    },
    {
      name: 'request-dependent fixed tiers with an extra multiplier',
      pricing: {
        billing_mode: 'tiered_expr',
        billing_expr:
          '(param("quality") == "hd" ? tier("hd", fixed(0.02)) : tier("base", fixed(0.01))) * (header("x-fast") == "true" ? 2 : 1)',
      },
      matches: ['request'],
    },
    {
      name: 'mixed pricing across nested time tiers classified only as token-based',
      pricing: {
        billing_mode: 'tiered_expr',
        billing_expr:
          'hour("UTC") < 12 ? (len < 1000 ? tier("short", fixed(0.01)) : tier("long", p * 2 + c * 8)) : tier("night", fixed(0.02))',
      },
      matches: ['token'],
    },
    {
      name: 'mixed pricing overriding a legacy request quota type',
      pricing: {
        quota_type: 1,
        billing_mode: 'tiered_expr',
        billing_expr:
          'len < 1000 ? tier("short", fixed(0.01)) : tier("long", p * 2 + c * 8)',
      },
      matches: ['token'],
    },
    {
      name: 'dynamic token pricing overriding a legacy request quota type',
      pricing: {
        quota_type: 1,
        billing_mode: 'tiered_expr',
        billing_expr: 'tier("base", p * 2 + c * 8)',
      },
      matches: ['token'],
    },
    {
      name: 'token pricing with fixed syntax only in its tier label',
      pricing: {
        billing_mode: 'tiered_expr',
        billing_expr: 'tier("fixed(0.01)", p * 2)',
      },
      matches: ['token'],
    },
    {
      name: 'task usage retaining its separate category',
      pricing: {
        billing_mode: 'tiered_expr',
        billing_expr: 'tier("base", u("seconds") * 0.4)',
        billing_usage_schema: { seconds: { type: 'number', unit: 'second' } },
      },
      matches: ['task'],
    },
    {
      name: 'a plugin model whose ordinary requests pay a token ratio',
      pricing: {
        billing_usage_schema: { seconds: { type: 'number', unit: 'second' } },
        ordinary_pricing: true,
        billing_plugin_variants: [
          {
            plugin_key: 'alpha',
            plugin_name: 'Alpha',
            billing_mode: 'tiered_expr',
            billing_expr: '',
            billing_usage_schema: {
              seconds: { type: 'number', unit: 'second' },
            },
          },
        ],
      },
      matches: ['token'],
    },
    {
      name: 'a plugin model without ordinary requests retaining the task category',
      pricing: {
        billing_usage_schema: { seconds: { type: 'number', unit: 'second' } },
        billing_plugin_variants: [
          {
            plugin_key: 'alpha',
            plugin_name: 'Alpha',
            billing_mode: 'tiered_expr',
            billing_expr: '',
            billing_usage_schema: {
              seconds: { type: 'number', unit: 'second' },
            },
          },
        ],
      },
      matches: ['task'],
    },
    {
      name: 'unparseable dynamic pricing retaining its legacy category',
      pricing: {
        quota_type: 1,
        billing_mode: 'tiered_expr',
        billing_expr: 'tier("request", fixed(',
      },
      matches: ['request'],
    },
  ])(
    'keeps filter results and sidebar counts consistent for $name',
    ({ pricing, matches }) => {
      const model: PricingModel = {
        id: 1,
        model_name: 'pricing-model',
        quota_type: 0,
        model_ratio: 1,
        completion_ratio: 1,
        enable_groups: ['default'],
        ...pricing,
      }
      const models = [model]
      render(<PricingSidebar {...filterProps()} models={models} />)

      for (const [quotaType, label] of [
        ['token', 'Token-based'],
        ['request', 'Per Request'],
        ['task', 'Task billing'],
      ]) {
        const included = matches.includes(quotaType)
        expect(filterByQuotaType(models, quotaType)).toEqual(
          included ? [model] : []
        )
        expect(
          screen.getByRole('button', {
            name: new RegExp(`^${label}\\s*${included ? 1 : 0}$`),
          })
        ).toBeVisible()
      }
      expect(filterByQuotaType(models, 'all')).toEqual([model])
      expect(
        within(screen.getByRole('group', { name: 'Pricing Type' })).getByRole(
          'button',
          { name: /^All\s*1$/ }
        )
      ).toBeVisible()
    }
  )

  it('counts platforms by allowed groups, lists exclusives under Other and shows their effective zero ratio', async () => {
    const props = filterProps()
    const { rerender } = render(<PricingSidebar {...props} />)
    expect(
      within(screen.getByRole('group', { name: 'Platform' })).getByRole(
        'button',
        { name: 'All 3' }
      )
    ).toBeVisible()
    const other = screen.getByRole('button', { name: 'Other 1' })
    await userEvent.click(other)
    expect(props.onPlatformChange).toHaveBeenCalledWith('other')
    rerender(<PricingSidebar {...props} platform='other' />)
    const row = within(screen.getByRole('group', { name: 'Group' }))
    const group = row.getByRole('button', {
      name: /Exclusive Benefit Private tier ×0/,
    })
    await userEvent.click(group)
    expect(props.onGroupChange).toHaveBeenCalledWith('vip')
    expect(row.queryByText('basic')).toBeNull()
  })

  it('identifies platform numbers as group counts even when there are no matching models', () => {
    render(<PricingSidebar {...filterProps()} />)
    expect(
      screen.getByRole('group', { name: 'Platform' })
    ).toHaveAccessibleDescription('Available group counts')
  })

  it('keeps the selected group visible when a long mobile list is collapsed', async () => {
    const media = window.matchMedia
    const query = vi
      .spyOn(window, 'matchMedia')
      .mockImplementation((value) => ({
        ...media(value),
        matches: value === '(max-width: 639px)' || media(value).matches,
      }))
    try {
      const props = filterProps()
      const groups = ['a', 'b', 'c', 'd', 'e', 'f', 'z']
      render(<PricingSidebar {...props} groups={groups} groupFilter='z' />)
      const row = within(screen.getByRole('group', { name: 'Group' }))
      const active = row.getByRole('button', { name: 'z' })
      expect(active).toHaveAttribute('aria-pressed', 'true')
      expect(row.queryByRole('button', { name: 'f' })).toBeNull()
      const expand = row.getByRole('button', { name: 'Expand' })
      expect(expand).toHaveAttribute('aria-expanded', 'false')
      expand.focus()
      await userEvent.keyboard('{Enter}')
      expect(row.getByRole('button', { name: 'f' })).toBeVisible()
      await userEvent.click(row.getByRole('button', { name: 'Collapse' }))
      expect(active).toBeVisible()
      expect(row.queryByRole('button', { name: 'f' })).toBeNull()
    } finally {
      query.mockRestore()
    }
  })

  it('counts duplicated endpoint declarations once and exposes custom protocol chips', () => {
    const props = filterProps()
    render(
      <PricingSidebar
        {...props}
        models={[
          {
            id: 1,
            model_name: 'custom',
            model_ratio: 1,
            completion_ratio: 1,
            quota_type: 0,
            enable_groups: ['basic'],
            supported_endpoint_types: ['audio-speech', 'audio-speech'],
          },
        ]}
      />
    )
    expect(screen.getByRole('button', { name: 'audio-speech 1' })).toBeVisible()
    expect(screen.queryByRole('group', { name: 'Model Tags' })).toBeNull()
  })

  it('announces filtered model totals and offers reset only when there is something to clear', async () => {
    const props = toolbarProps()
    const reset = vi.fn()
    const { rerender } = render(
      <PricingToolbar {...props} onClearFilters={reset} />
    )
    expect(screen.getByRole('status')).toHaveTextContent('Matching models2')
    expect(screen.queryByRole('button', { name: 'Reset filters' })).toBeNull()
    rerender(
      <PricingToolbar
        {...props}
        filteredCount={0}
        hasActiveFilters
        onClearFilters={reset}
      />
    )
    expect(screen.getByRole('status')).toHaveTextContent('Matching models0/ 2')
    await userEvent.click(screen.getByRole('button', { name: 'Reset filters' }))
    expect(reset).toHaveBeenCalledOnce()
  })

  it('focuses the standalone search with Ctrl+K and returns focus to it after clearing', async () => {
    const onClear = vi.fn()
    render(<SearchBar value='sample' onChange={vi.fn()} onClear={onClear} />)
    const search = within(screen.getByRole('search', { name: 'Search models' }))
    const field = search.getByRole('textbox', { name: 'Search models' })
    const user = userEvent.setup()
    await user.keyboard('{Control>}k{/Control}')
    expect(field).toHaveFocus()
    await user.click(search.getByRole('button', { name: 'Clear search' }))
    expect(onClear).toHaveBeenCalledOnce()
    expect(field).toHaveFocus()
  })

  it('changes the token unit and keeps the selected unit pressed when clicked again', async () => {
    const props = toolbarProps()
    const user = userEvent.setup()
    const { rerender } = render(<PricingToolbar {...props} />)
    await user.click(screen.getByRole('button', { name: '/1K' }))
    expect(props.onTokenUnitChange).toHaveBeenCalledWith('K')
    rerender(<PricingToolbar {...props} tokenUnit='K' />)
    const selected = screen.getByRole('button', { name: '/1K' })
    expect(selected).toHaveAttribute('aria-pressed', 'true')
    await user.click(selected)
    expect(selected).toHaveAttribute('aria-pressed', 'true')
    expect(props.onTokenUnitChange).toHaveBeenCalledTimes(1)
  })

  it('changes the recharge display mode with an accessible selected state', async () => {
    const props = toolbarProps()
    const user = userEvent.setup()
    const { rerender } = render(<PricingToolbar {...props} />)
    await user.click(screen.getByRole('button', { name: 'Recharge' }))
    expect(props.onRechargePriceChange).toHaveBeenCalledWith(true)
    rerender(<PricingToolbar {...props} showRechargePrice />)
    expect(screen.getByRole('button', { name: 'Recharge' })).toHaveAttribute(
      'aria-pressed',
      'true'
    )
    expect(screen.getByRole('button', { name: 'Standard' })).toHaveAttribute(
      'aria-pressed',
      'false'
    )
  })

  it('switches to table view with the keyboard and exposes the selected view', async () => {
    const props = toolbarProps()
    const user = userEvent.setup()
    const { rerender } = render(<PricingToolbar {...props} />)
    const tableButton = screen.getByRole('button', { name: 'Table view' })
    tableButton.focus()
    await user.keyboard('{Enter}')
    expect(props.onViewModeChange).toHaveBeenCalledWith('table')
    rerender(<PricingToolbar {...props} viewMode='table' />)
    expect(tableButton).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: 'Card view' })).toHaveAttribute(
      'aria-pressed',
      'false'
    )
  })

  it('selects price sorting from the shared dropdown', async () => {
    const props = toolbarProps()
    const user = userEvent.setup()
    render(<PricingToolbar {...props} />)
    await user.click(screen.getByRole('button', { name: 'Name' }))
    await user.click(
      screen.getByRole('menuitem', { name: 'Input Price: Low to High' })
    )
    expect(props.onSortChange).toHaveBeenCalledWith('input-price-low')
  })

  it('lists the model order sorts above the name and price sorts', async () => {
    const props = { ...toolbarProps(), sortBy: 'recommended' }
    const user = userEvent.setup()
    render(<PricingToolbar {...props} />)
    await user.click(screen.getByRole('button', { name: 'Recommended' }))
    const [orderGroup, otherGroup] = within(
      await screen.findByRole('menu')
    ).getAllByRole('group')
    expect(
      within(orderGroup)
        .getAllByRole('menuitem')
        .map((item) => item.textContent)
    ).toEqual(['Recommended', 'Most requests', 'Most tokens'])
    expect(within(otherGroup).getAllByRole('menuitem')[0]).toHaveTextContent(
      'Name'
    )
    await user.click(
      within(orderGroup).getByRole('menuitem', { name: 'Most requests' })
    )
    expect(props.onSortChange).toHaveBeenCalledWith('requests')
  })
})

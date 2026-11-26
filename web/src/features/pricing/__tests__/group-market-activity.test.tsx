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
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { GroupMarketBadges } from '../components/group-market-activity'
import { ModelDetailsContent } from '../components/model-details'
import {
  PricingSidebar,
  type PricingSidebarProps,
} from '../components/pricing-sidebar'
import { getRecentGroupActivity } from '../lib/group-market-activity'
import type { GroupMarketActivity } from '../types'

vi.mock('@visactor/react-vchart', () => ({ VChart: () => null }))

const latest = { old_ratio: 0.3, new_ratio: 0.2, effective_at: 1795521600 }
const earlier = { old_ratio: 0, new_ratio: 0.3, effective_at: 1795435200 }
const activity: GroupMarketActivity = {
  first_configured_at: earlier.effective_at,
  is_new: true,
  rate_changes: [latest, earlier],
  recent_rate_change: true,
}
function sidebar(): PricingSidebarProps {
  return {
    platform: 'all',
    groupFilter: 'all',
    quotaTypeFilter: 'all',
    endpointTypeFilter: 'all',
    groups: ['alpha', 'beta'],
    groupRatios: { alpha: 0.2, beta: 0 },
    groupMarketActivity: { alpha: activity },
    models: [],
    onGroupChange: vi.fn(),
    onPlatformChange: vi.fn(),
    onQuotaTypeChange: vi.fn(),
    onEndpointTypeChange: vi.fn(),
  }
}
describe('recorded group activity', () => {
  it('shows only a new badge and the signed magnitude in the group filter without changing keyboard selection', async () => {
    const props = sidebar()
    render(<PricingSidebar {...props} />)
    const group = screen.getByRole('group', { name: 'Group' })
    const active = within(group).getByRole('button', { name: 'alpha ×0.2' })
    expect(active).toHaveAccessibleDescription('New −0.1×')
    expect(within(active).getByText('New')).toBeVisible()
    expect(within(active).getByText('0.1×')).toBeVisible()
    expect(within(active).getByText('−0.1×')).toHaveClass('sr-only')
    expect(active.querySelector('.lucide-arrow-down')).toBeVisible()
    expect(active).not.toHaveTextContent(/\d{4}-\d{2}-\d{2}|→|Base multiplier/)
    expect(active.querySelector('time')).not.toBeInTheDocument()
    expect(active.querySelector('[title]')).not.toBeInTheDocument()
    const beta = within(group).getByRole('button', { name: 'beta ×0' })
    expect(beta).not.toHaveAttribute('aria-describedby')
    active.focus()
    await userEvent.keyboard('{Enter}')
    expect(props.onGroupChange).toHaveBeenCalledWith('alpha')
  })

  it('does not advertise older recorded dates or changes as recent', () => {
    render(
      <PricingSidebar
        {...sidebar()}
        groupMarketActivity={{
          alpha: {
            first_configured_at: earlier.effective_at,
            rate_changes: [latest],
          },
        }}
      />
    )
    const chip = screen.getByRole('button', { name: 'alpha ×0.2' })
    expect(within(chip).queryByText('New')).not.toBeInTheDocument()
    expect(chip).not.toHaveAttribute('aria-describedby')
  })

  it.each([undefined, 'tier("base", p * 2 + c * 8)'])(
    'shows only compact activity badges in ordinary and dynamic model quotes (expression: %s)',
    (billing_expr) => {
      vi.spyOn(api, 'get').mockResolvedValue({ data: { data: { groups: [] } } })
      const client = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 } },
      })
      const { container, unmount } = render(
        <QueryClientProvider client={client}>
          <ModelDetailsContent
            model={{
              id: 1,
              model_name: 'example',
              quota_type: 0,
              model_ratio: 1,
              completion_ratio: 1,
              enable_groups: ['alpha'],
              group_market_activity: { alpha: activity },
              billing_mode: billing_expr ? 'tiered_expr' : undefined,
              billing_expr,
            }}
            groupRatio={{ alpha: 0.2 }}
            usableGroup={{ alpha: { desc: '', ratio: 0.2 } }}
            endpointMap={{}}
            autoGroups={[]}
            priceRate={1}
            usdExchangeRate={1}
            tokenUnit='M'
          />
        </QueryClientProvider>
      )
      try {
        expect(screen.getByText('New')).toBeVisible()
        expect(screen.getByText('0.1×')).toBeVisible()
        expect(screen.getByText('−0.1×')).toHaveClass('sr-only')
        expect(screen.getByText('0.2x')).toBeVisible()
        expect(container).not.toHaveTextContent(
          /\d{4}-\d{2}-\d{2}|→|Base multiplier|\+0.3×/
        )
        expect(container.querySelector('time')).not.toBeInTheDocument()
        expect(
          screen.queryByRole('button', { name: /history/i })
        ).not.toBeInTheDocument()
      } finally {
        unmount()
        client.clear()
      }
    }
  )

  it('does not show older activity in details', () => {
    const { container } = render(
      <GroupMarketBadges
        activity={getRecentGroupActivity({
          first_configured_at: earlier.effective_at,
          rate_changes: [latest],
        })}
      />
    )
    expect(container).toBeEmptyDOMElement()
  })

  it.each([
    [1.00000001, 1.00000002, '+0.00000001'],
    [0.0000000001, 0, '−0.0000000001'],
    [1.25, 0.25, '−1'],
    [0, 0.3, '+0.3'],
    [1, 101, '+100'],
  ] as const)(
    'shows the actual signed difference from %s to %s',
    (old_ratio, new_ratio, delta) => {
      const { container } = render(
        <GroupMarketBadges
          activity={getRecentGroupActivity({
            recent_rate_change: true,
            rate_changes: [{ ...latest, old_ratio, new_ratio }],
          })}
        />
      )
      expect(screen.getByText(`${delta.slice(1)}×`)).toBeVisible()
      expect(screen.getByText(`${delta}×`)).toHaveClass('sr-only')
      const icon = old_ratio < new_ratio ? 'arrow-up' : 'arrow-down'
      expect(container.querySelector(`.lucide-${icon}`)).toBeVisible()
    }
  )
})

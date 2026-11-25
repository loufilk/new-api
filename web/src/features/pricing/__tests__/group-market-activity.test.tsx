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

import {
  GroupQuoteActivity,
  MarketRateChangeLine,
} from '../components/group-market-activity'
import {
  PricingSidebar,
  type PricingSidebarProps,
} from '../components/pricing-sidebar'
import type { GroupMarketActivity } from '../types'

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
  it('shows the dated change on the filter itself and keeps the group selectable by keyboard', async () => {
    const props = sidebar()
    render(<PricingSidebar {...props} />)
    const group = screen.getByRole('group', { name: 'Group' })
    const active = within(group).getByRole('button', { name: 'alpha ×0.2' })
    expect(active).toHaveAccessibleDescription(
      /New.*Base multiplier −0.1×.*From 0.3× to 0.2×/
    )
    expect(within(active).getByText('0.3× → 0.2×')).toBeVisible()
    expect(within(active).getByText('Base multiplier −0.1×')).toBeVisible()
    expect(
      [...active.querySelectorAll('time')].map((el) => el.dateTime)
    ).toEqual([
      new Date(earlier.effective_at * 1000).toISOString(),
      new Date(latest.effective_at * 1000).toISOString(),
    ])
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

  it('opens matching group history by keyboard and preserves an increase from zero', async () => {
    const user = userEvent.setup()
    const { rerender } = render(
      <GroupQuoteActivity activity={activity} label='Alpha' />
    )
    const toggle = screen.getByRole('button', {
      name: 'Multiplier history for Alpha',
    })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('list')).not.toBeInTheDocument()
    toggle.focus()
    await user.keyboard('[Space]')
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    const rows = within(screen.getByRole('list')).getAllByRole('listitem')
    expect(rows[0]).toHaveTextContent('0.3× → 0.2×')
    expect(rows[1]).toHaveTextContent('Base multiplier +0.3×')
    expect(rows[1]).toHaveTextContent('0× → 0.3×')
    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    rerender(<GroupQuoteActivity label='No history' />)
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(screen.queryByText('New')).not.toBeInTheDocument()
  })

  it.each([
    [1.00000001, 1.00000002, '+0.00000001'],
    [0.0000000001, 0, '−0.0000000001'],
    [1.25, 0.25, '−1'],
    [1, 101, '+100'],
  ] as const)(
    'shows the actual signed difference from %s to %s',
    (old_ratio, new_ratio, delta) => {
      render(
        <MarketRateChangeLine change={{ ...latest, old_ratio, new_ratio }} />
      )
      expect(screen.getByText(`Base multiplier ${delta}×`)).toBeVisible()
    }
  )
})

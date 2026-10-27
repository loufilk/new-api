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
import { render } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import type { UserWalletData, WalletSummary } from '../../types'
import { WalletStatsCard } from '../wallet-stats-card'

const user: UserWalletData = {
  id: 7,
  username: 'wallet-user',
  quota: 9_999_999,
  used_quota: 1_500_000,
  request_count: 12_345,
  aff_count: 0,
  group: 'default',
}

const summary: WalletSummary = {
  total: 2_000_000,
  paid: 1_000_000,
  bonus: 500_000,
  reward: 500_000,
  rules: {
    signup_bonus_enabled: true,
    signup_bonus_quota: 500_000,
    referral_bonus_enabled: true,
    referral_bonus_rate: 0.05,
  },
}

function getStat(container: HTMLElement, key: string): HTMLElement {
  const stat = container.querySelector<HTMLElement>(
    `[data-wallet-stat="${key}"]`
  )
  expect(stat).not.toBeNull()
  return stat as HTMLElement
}

describe('wallet stats card', () => {
  test('renders paid, bonus, usage, and request metrics in the required order', () => {
    const { container } = render(
      <WalletStatsCard user={user} summary={summary} />
    )

    const stats = [
      ...container.querySelectorAll<HTMLElement>('[data-wallet-stat]'),
    ]
    expect(stats.map((stat) => stat.dataset.walletStat)).toEqual([
      'paid',
      'bonus',
      'usage',
      'requests',
    ])

    expect(getStat(container, 'paid')).toHaveTextContent('Paid Balance')
    expect(getStat(container, 'paid')).toHaveTextContent('$2.00')
    expect(getStat(container, 'bonus')).toHaveTextContent('Bonus Balance')
    expect(getStat(container, 'bonus')).toHaveTextContent('$2.00')
    expect(getStat(container, 'bonus')).toHaveTextContent(
      'Signup bonus applies to eligible models; invitation rewards apply to all models'
    )
    expect(getStat(container, 'usage')).toHaveTextContent('Total Usage')
    expect(getStat(container, 'usage')).toHaveTextContent('$3.00')
    expect(getStat(container, 'requests')).toHaveTextContent('API Requests')
    expect(getStat(container, 'requests')).toHaveTextContent('12,345')
    expect(container).not.toHaveTextContent('Current Balance')
    expect(container).not.toHaveTextContent('Total Balance')
  })

  test('loads balance and user metrics independently', () => {
    const { container, rerender } = render(
      <WalletStatsCard
        user={user}
        summary={summary}
        summaryLoading
        userLoading={false}
      />
    )

    expect(
      getStat(container, 'paid').querySelectorAll('[data-slot="skeleton"]')
    ).toHaveLength(3)
    expect(
      getStat(container, 'bonus').querySelectorAll('[data-slot="skeleton"]')
    ).toHaveLength(3)
    expect(getStat(container, 'usage')).toHaveTextContent('$3.00')
    expect(getStat(container, 'requests')).toHaveTextContent('12,345')

    rerender(
      <WalletStatsCard
        user={user}
        summary={summary}
        summaryLoading={false}
        userLoading
      />
    )

    expect(getStat(container, 'paid')).toHaveTextContent('$2.00')
    expect(getStat(container, 'bonus')).toHaveTextContent('$2.00')
    expect(
      getStat(container, 'usage').querySelectorAll('[data-slot="skeleton"]')
    ).toHaveLength(3)
    expect(
      getStat(container, 'requests').querySelectorAll('[data-slot="skeleton"]')
    ).toHaveLength(3)
  })

  test('uses a two-column mobile grid and a four-column desktop grid with internal dividers', () => {
    const { container } = render(
      <WalletStatsCard user={user} summary={summary} />
    )

    const grid = container.querySelector<HTMLElement>(
      '[data-wallet-stats-grid]'
    )
    expect(grid).toHaveClass('grid-cols-2', 'lg:grid-cols-4', 'overflow-hidden')

    expect(getStat(container, 'paid')).not.toHaveClass('border-l', 'border-t')
    expect(getStat(container, 'bonus')).toHaveClass('border-l')
    expect(getStat(container, 'usage')).toHaveClass(
      'border-t',
      'lg:border-t-0',
      'lg:border-l'
    )
    expect(getStat(container, 'requests')).toHaveClass(
      'border-l',
      'border-t',
      'lg:border-t-0',
      'lg:border-l'
    )

    for (const value of container.querySelectorAll(
      '[data-wallet-stat-value]'
    )) {
      expect(value).toHaveClass('break-all', 'tabular-nums')
    }
  })
})

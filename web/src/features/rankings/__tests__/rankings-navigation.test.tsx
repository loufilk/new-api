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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'

import { RankingsHero } from '../components/rankings-hero'
import type { RankingPeriod } from '../types'

function Hero() {
  const [period, setPeriod] = useState<RankingPeriod>('week')
  return <RankingsHero period={period} onPeriodChange={setPeriod} />
}

describe('ranking period navigation', () => {
  it('supports arrow-key switching without tabbing through every period', async () => {
    const user = userEvent.setup()
    render(<Hero />)
    const week = screen.getByRole('tab', { name: 'Week' })
    week.focus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('tab', { name: 'Month' })).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(screen.getByRole('tab', { name: 'Month' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
  })
})

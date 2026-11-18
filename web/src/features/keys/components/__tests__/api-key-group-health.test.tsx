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
import { expect, test } from 'vitest'

import { ApiKeyGroupHealth } from '../api-key-group-health'

test.each([90, 1, 0, 100])(
  'displays the API percentage %s without converting it to a ratio',
  (rate) => {
    render(
      <ApiKeyGroupHealth
        detail={{
          name: 'group',
          ratio: 1,
          available: true,
          success_rate: rate,
          history: [rate],
        }}
      />
    )
    expect(screen.getByText(`${rate}% success rate`)).toBeVisible()
    expect(screen.queryByText('No performance data')).not.toBeInTheDocument()
  }
)

test('missing samples stay unknown instead of displaying a zero success rate', () => {
  render(
    <ApiKeyGroupHealth
      detail={{
        name: 'empty',
        ratio: 1,
        available: true,
        success_rate: null,
        history: [],
      }}
    />
  )
  expect(screen.getByText('No performance data')).toBeVisible()
  expect(screen.queryByText('0% success rate')).not.toBeInTheDocument()
})

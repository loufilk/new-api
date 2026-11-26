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
import { expect, it, vi } from 'vitest'

import { PricingTable } from '../components/pricing-table'
import type { PricingModel } from '../types'

const row: PricingModel = {
  id: 1,
  model_name: 'provider/example',
  quota_type: 0,
  model_ratio: 1,
  completion_ratio: 3,
  vendor_name: 'Vendor',
  tags: 'reasoning',
  enable_groups: ['elsewhere', 'public', 'premium'],
  group_ratio: { elsewhere: 0.1, public: 1, premium: 2 },
  output_modalities: ['text'],
  supported_endpoint_types: ['openai', 'missing'],
}

it('opens the model from the keyboard, while copy and table scrolling stay local to the list', async () => {
  const user = userEvent.setup()
  const open = vi.fn()
  render(
    <PricingTable
      models={[row]}
      onModelClick={open}
      endpointMap={{ openai: { path: '/v1/chat/completions' } }}
      platformPriceGroups={new Set(['public', 'premium'])}
    />
  )
  const table = screen.getByRole('table')
  expect(table.closest('[data-slot="table-container"]')).toHaveClass(
    'overflow-x-auto'
  )
  for (const header of [
    'Model',
    'Type',
    'Price',
    'Cached',
    'Vendor',
    'Tags',
    'Endpoints',
    'Groups',
  ]) {
    expect(
      within(table).getByRole('columnheader', { name: header })
    ).toBeVisible()
  }
  expect(within(table).getByText('public')).toBeVisible()
  expect(within(table).getByText('+1')).toBeVisible()
  expect(within(table).queryByText('missing')).not.toBeInTheDocument()
  await user.click(
    within(table).getByRole('button', { name: 'Copy model name' })
  )
  expect(await navigator.clipboard.readText()).toBe(row.model_name)
  expect(open).not.toHaveBeenCalled()
  within(table).getByRole('button', { name: row.model_name }).focus()
  await user.keyboard('{Enter}')
  expect(open).toHaveBeenCalledOnce()
  expect(open).toHaveBeenCalledWith(row.model_name)
})

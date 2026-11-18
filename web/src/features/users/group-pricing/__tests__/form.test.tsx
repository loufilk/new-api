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
import {
  cleanup,
  fireEvent,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { config, renderPricing, setupPricingRequests } from './fixtures'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it('adds one group rule through keyboard selection and saves only that group', async () => {
  const { put } = setupPricingRequests({ ...config, ratios: {} })
  renderPricing()
  await screen.findByText('Uses default group pricing')
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Add group pricing' }))
  const group = screen.getByRole('combobox', { name: 'Billing group' })
  await user.click(group)
  await user.type(group, 'premium')
  await user.keyboard('{ArrowDown}{Enter}')
  expect(group).toHaveValue('premium')
  await user.type(
    screen.getByRole('spinbutton', { name: 'Personal multiplier' }),
    '0.4'
  )
  await user.click(
    screen.getByRole('button', { name: 'Save personal pricing' })
  )
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/user/12/group-pricing', {
      expected_revision: 1,
      ratios: { premium: 0.4 },
    })
  )
})

it('shows 0.4 as 66.67 percent of default 0.6 and allows a personal multiplier above the default', async () => {
  const { put } = setupPricingRequests()
  renderPricing()
  const input = await screen.findByRole('spinbutton', {
    name: 'Personal multiplier',
  })
  expect(screen.getByText('66.67% of the default charge')).toBeInTheDocument()
  fireEvent.change(input, { target: { value: '1.2' } })
  expect(screen.getByText('200% of the default charge')).toBeInTheDocument()
  await userEvent.click(
    screen.getByRole('button', { name: 'Save personal pricing' })
  )
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/user/12/group-pricing', {
      expected_revision: 1,
      ratios: { premium: 1.2 },
    })
  )
})

it('rejects an empty multiplier without treating it as free pricing', async () => {
  const { put } = setupPricingRequests()
  renderPricing()
  fireEvent.change(
    await screen.findByRole('spinbutton', { name: 'Personal multiplier' }),
    { target: { value: '' } }
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'Save personal pricing' })
  )
  expect(
    await screen.findByText(
      'Enter a personal multiplier or remove the rule to inherit'
    )
  ).toBeInTheDocument()
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(put).not.toHaveBeenCalled()
})

it('requires explicit confirmation for zero and saves zero as a real personal multiplier', async () => {
  const { put } = setupPricingRequests()
  renderPricing()
  fireEvent.change(
    await screen.findByRole('spinbutton', { name: 'Personal multiplier' }),
    { target: { value: '0' } }
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'Save personal pricing' })
  )
  const dialog = await screen.findByRole('alertdialog')
  expect(within(dialog).getByText(/premium/)).toBeInTheDocument()
  expect(put).not.toHaveBeenCalled()
  await userEvent.click(
    within(dialog).getByRole('button', { name: 'Confirm free billing' })
  )
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/user/12/group-pricing', {
      expected_revision: 1,
      ratios: { premium: 0 },
    })
  )
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(
    screen.getByRole('spinbutton', { name: 'Personal multiplier' })
  ).toHaveValue(0)
})

it('restores one group to default pricing while preserving the other personal rule', async () => {
  const { put } = setupPricingRequests({
    ...config,
    ratios: { premium: 0.4, default: 0.8 },
  })
  renderPricing()
  const first = await screen.findByRole('group', { name: 'Pricing rule 1' })
  await userEvent.click(
    within(first).getByRole('button', { name: 'Restore default pricing' })
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'Save personal pricing' })
  )
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/user/12/group-pricing', {
      expected_revision: 1,
      ratios: { default: 0.8 },
    })
  )
})

it('stages restoring every group and sends an empty rule map only when saved', async () => {
  const { put } = setupPricingRequests()
  renderPricing()
  await screen.findByRole('spinbutton', { name: 'Personal multiplier' })
  await userEvent.click(
    screen.getByRole('button', { name: 'Restore all default pricing' })
  )
  expect(screen.getByText('Uses default group pricing')).toBeInTheDocument()
  expect(put).not.toHaveBeenCalled()
  await userEvent.click(
    screen.getByRole('button', { name: 'Save personal pricing' })
  )
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/user/12/group-pricing', {
      expected_revision: 1,
      ratios: {},
    })
  )
})

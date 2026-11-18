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
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { config, renderPricing, setupPricingRequests } from './fixtures'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it('preserves a conflicting draft, compares the latest values, and saves with the reviewed revision', async () => {
  const { get, put } = setupPricingRequests()
  put.mockRejectedValueOnce({
    response: {
      status: 409,
      data: {
        success: false,
        message: 'User group pricing changed; reload and try again',
      },
    },
  })
  renderPricing()
  const input = await screen.findByRole('spinbutton', {
    name: 'Personal multiplier',
  })
  fireEvent.change(input, { target: { value: '0.2' } })
  await userEvent.click(
    screen.getByRole('button', { name: 'Save personal pricing' })
  )
  expect(
    await screen.findByText(
      'Personal group pricing changed. Reload and compare before saving.'
    )
  ).toBeInTheDocument()
  expect(input).toHaveValue(0.2)
  expect(
    screen.getByRole('button', { name: 'Save personal pricing' })
  ).toBeDisabled()

  get.mockResolvedValue({
    data: {
      success: true,
      data: { ...config, revision: 2, ratios: { premium: 0.3, default: 0.9 } },
    },
  })
  await userEvent.click(
    screen.getByRole('button', { name: 'Reload and compare' })
  )
  await screen.findByRole('columnheader', { name: 'Latest saved pricing' })
  expect(input).toHaveValue(0.2)
  expect(screen.getByText('0.3×')).toBeInTheDocument()
  expect(put).toHaveBeenCalledTimes(1)

  await userEvent.click(
    screen.getByRole('button', { name: 'Keep my draft for review' })
  )
  expect(input).toHaveValue(0.2)
  await userEvent.click(
    screen.getByRole('button', { name: 'Save personal pricing' })
  )
  await waitFor(() =>
    expect(put).toHaveBeenLastCalledWith('/api/user/12/group-pricing', {
      expected_revision: 2,
      ratios: { premium: 0.2 },
    })
  )
})

it('discards a draft only after the operator chooses the latest saved pricing', async () => {
  const { get, put } = setupPricingRequests()
  renderPricing()
  fireEvent.change(
    await screen.findByRole('spinbutton', { name: 'Personal multiplier' }),
    { target: { value: '0.2' } }
  )
  get.mockResolvedValue({
    data: {
      success: true,
      data: { ...config, revision: 2, ratios: { premium: 0.3 } },
    },
  })
  await userEvent.click(
    screen.getByRole('button', { name: 'Reload and compare' })
  )
  await screen.findByRole('columnheader', { name: 'Latest saved pricing' })
  expect(
    screen.getByRole('spinbutton', { name: 'Personal multiplier' })
  ).toHaveValue(0.2)
  await userEvent.click(
    screen.getByRole('button', { name: 'Use latest saved pricing' })
  )
  expect(
    screen.getByRole('spinbutton', { name: 'Personal multiplier' })
  ).toHaveValue(0.3)
  expect(
    screen.getByRole('button', { name: 'Save personal pricing' })
  ).toBeDisabled()
  expect(put).not.toHaveBeenCalled()
})

it('loads a different user without carrying across the previous user draft', async () => {
  const { get, put } = setupPricingRequests()
  get.mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/user/13/group-pricing'
          ? { ...config, user_id: 13, ratios: { premium: 0.7 } }
          : config,
    },
  }))
  const rendered = renderPricing()
  fireEvent.change(
    await screen.findByRole('spinbutton', { name: 'Personal multiplier' }),
    { target: { value: '0.2' } }
  )
  rendered.rerender(rendered.view(13))
  await waitFor(() =>
    expect(
      screen.getByRole('spinbutton', { name: 'Personal multiplier' })
    ).toHaveValue(0.7)
  )
  expect(
    screen.getByRole('button', { name: 'Save personal pricing' })
  ).toBeDisabled()
  expect(put).not.toHaveBeenCalled()
})

it('shows a failed read with a retry action and never presents it as inherited pricing', async () => {
  const { get } = setupPricingRequests()
  get.mockRejectedValueOnce(new Error('Temporary connection failure'))
  renderPricing()
  await screen.findByText('Failed to load personal group pricing')
  expect(
    screen.queryByText('Uses default group pricing')
  ).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(
    await screen.findByRole('spinbutton', { name: 'Personal multiplier' })
  ).toHaveValue(0.4)
})

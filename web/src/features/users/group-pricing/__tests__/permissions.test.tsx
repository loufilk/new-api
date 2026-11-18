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
import { cleanup, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { renderPricing, setupPricingRequests } from './fixtures'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it.each([
  { role: 1, targetRole: 1, read: true },
  { role: 10, targetRole: 10, read: true },
  { role: 10, targetRole: 100, read: true },
  { role: 10, targetRole: 1, read: false },
])(
  'does not load pricing when role $role cannot read target role $targetRole with read=$read',
  (permissions) => {
    const { get } = setupPricingRequests()
    renderPricing(permissions)
    expect(screen.queryByText('Personal group pricing')).not.toBeInTheDocument()
    expect(get).not.toHaveBeenCalled()
  }
)

it('lets an administrator with read permission inspect lower-role pricing without edit controls', async () => {
  setupPricingRequests()
  renderPricing({ role: 10, read: true, update: false })
  expect(
    await screen.findByRole('spinbutton', { name: 'Personal multiplier' })
  ).toBeDisabled()
  expect(
    screen.queryByRole('button', { name: 'Save personal pricing' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Add group pricing' })
  ).not.toBeInTheDocument()
})

it('lets root edit a root target even when ordinary permission grants are absent', async () => {
  setupPricingRequests()
  renderPricing({ role: 100, targetRole: 100, read: false, update: false })
  expect(
    await screen.findByRole('spinbutton', { name: 'Personal multiplier' })
  ).toBeEnabled()
  expect(
    screen.getByRole('button', { name: 'Save personal pricing' })
  ).toBeInTheDocument()
})

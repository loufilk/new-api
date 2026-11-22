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

import { renderInterception, setupRequests } from './fixtures'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it.each([
  { role: 1, read: true, update: true },
  { role: 5, read: true, update: true },
  { role: 10, read: false, update: true },
  { role: 10, read: true, update: true, targetRole: 10 },
])(
  'hides interception data and makes no requests for unauthorized viewer %j',
  (options) => {
    const { get } = setupRequests()
    renderInterception(options)
    expect(
      screen.queryByText('User-level call interception and custom replies')
    ).not.toBeInTheDocument()
    expect(get).not.toHaveBeenCalled()
  }
)

it('allows read-only administrators to inspect settings without editing or saving', async () => {
  setupRequests()
  renderInterception({ role: 10, read: true, update: false })
  expect(
    await screen.findByRole('textbox', { name: 'Custom reply' })
  ).toBeDisabled()
  expect(
    screen.getByRole('switch', { name: 'Enable call interception' })
  ).toHaveAttribute('aria-disabled', 'true')
  expect(
    screen.queryByRole('button', { name: 'Save interception settings' })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('tab', { name: 'Interception hits' })).toBeEnabled()
})

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
import { afterEach, expect, test, vi } from 'vitest'

import '@/extensions/bootstrap'
import { Route as CustomerRoute } from '@/routes/_authenticated/invoice'
import { Route as ManagementRoute } from '@/routes/_authenticated/invoice-management'
import { useAuthStore } from '@/stores/auth-store'

vi.mock('@/extensions/chuanapi/invoice/customer', () => ({
  default: () => <p>Customer invoice fixture</p>,
}))
vi.mock('@/extensions/chuanapi/invoice/admin', () => ({
  default: () => <p>Invoice management fixture</p>,
}))

afterEach(() => useAuthStore.getState().auth.reset())

test.each([
  ['Customer invoice fixture', CustomerRoute],
  ['Invoice management fixture', ManagementRoute],
] as const)(
  'the route loads its registered invoice page: %s',
  async (expected, route) => {
    const Page = route.options.component
    if (!Page?.preload) throw new Error('Missing lazy invoice route')
    await Page.preload()
    render(<Page />)
    expect(screen.getByText(expected)).toBeInTheDocument()
  }
)

test.each([1, 5, 10, 100])(
  'only root can enter invoice management (role %i)',
  (role) => {
    useAuthStore.getState().auth.setUser({ id: 7, username: 'fixture', role })
    const guard = ManagementRoute.options.beforeLoad as () => unknown
    if (role === 100) expect(guard).not.toThrow()
    else expect(guard).toThrow()
  }
)

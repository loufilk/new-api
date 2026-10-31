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
import { renderHook } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { useSidebarData } from '../use-sidebar-data'

afterEach(() => useAuthStore.getState().auth.reset())

it('retains wallet, API keys, models and security navigation for legacy agents', () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 7, username: 'agent', role: ROLE.AGENT })
  const { result } = renderHook(() => useSidebarData())
  expect(
    result.current.navGroups.flatMap((group) =>
      group.items.map((item) => 'url' in item && item.url)
    )
  ).toEqual(
    expect.arrayContaining([
      '/wallet',
      '/keys',
      '/playground',
      '/profile',
      '/security',
      '/redemption-codes',
      '/agent-redemption',
    ])
  )
})

it('preserves administrator workspace navigation including agent management', () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'admin', role: ROLE.ADMIN })
  const { result } = renderHook(() => useSidebarData())
  expect(
    result.current.navGroups.find((group) => group.id === 'admin')?.items
  ).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ url: '/agent-redemption' }),
      expect.objectContaining({ url: '/users' }),
    ])
  )
})

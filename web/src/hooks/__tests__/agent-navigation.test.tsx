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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, expect, it } from 'vitest'

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { useSidebarData } from '../use-sidebar-data'

function wrapper(props: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], {})
  return (
    <QueryClientProvider client={client}>{props.children}</QueryClientProvider>
  )
}

afterEach(() => useAuthStore.getState().auth.reset())

it('shows the simplified agent entries without legacy agent workspaces', () => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'agent', role: 5 })
  const { result } = renderHook(() => useSidebarData(), { wrapper })
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
      '/agent-platform',
      '/redemption-codes?view=center',
      '/redemption-codes?view=records',
    ])
  )
  const urls = result.current.navGroups.flatMap((group) =>
    group.items.map((item) => ('url' in item ? item.url : undefined))
  )
  expect(urls).not.toContain('/agent-redemption')
})

it('preserves administrator workspace navigation including agent management', () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'admin', role: ROLE.ADMIN })
  const { result } = renderHook(() => useSidebarData(), { wrapper })
  expect(
    result.current.navGroups.find((group) => group.id === 'admin')?.items
  ).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ url: '/agent-platform/finance' }),
      expect.objectContaining({ url: '/users' }),
    ])
  )
})

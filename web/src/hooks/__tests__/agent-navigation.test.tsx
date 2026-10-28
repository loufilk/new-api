import { renderHook } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { useSidebarData } from '../use-sidebar-data'

afterEach(() => useAuthStore.getState().auth.reset())

it('shows agents only the two dedicated workspace entries', () => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'agent', role: ROLE.AGENT })
  const { result } = renderHook(() => useSidebarData())
  expect(result.current.navGroups.flatMap((group) => group.items.map((item) => 'url' in item && item.url))).toEqual(['/redemption-codes', '/agent-redemption'])
})

it('preserves administrator workspace navigation including agent management', () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: ROLE.ADMIN })
  const { result } = renderHook(() => useSidebarData())
  expect(result.current.navGroups.find((group) => group.id === 'admin')?.items).toEqual(expect.arrayContaining([expect.objectContaining({ url: '/agent-redemption' }), expect.objectContaining({ url: '/users' })]))
})

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
import { afterEach, expect, it } from 'vitest'

import { hasAgentAdministration } from '@/lib/agent-permissions'
import { ROLE } from '@/lib/roles'
import { Route } from '@/routes/_authenticated/redemption-codes'
import { useAuthStore } from '@/stores/auth-store'

const guard = Route.options.beforeLoad as () => unknown

afterEach(() => useAuthStore.getState().auth.reset())

it.each([undefined, ROLE.GUEST, ROLE.USER, Number.NaN])(
  'identity with role %s cannot open redemption administration',
  (role) => {
    useAuthStore
      .getState()
      .auth.setUser(
        role === undefined ? null : { id: 1, username: 'user', role }
      )
    let rejection: unknown
    try {
      guard()
    } catch (error) {
      rejection = error
    }
    expect(rejection).toMatchObject({ options: { to: '/403' } })
  }
)

it.each([
  { id: 7, username: 'legacy-agent', role: 5 },
  {
    id: 7,
    username: 'migrated-agent',
    role: ROLE.USER,
    permissions: { agent_history: true },
  },
])(
  'authorized A07 agent $username can open its code workspace without finance authority',
  (user) => {
    useAuthStore.getState().auth.setUser(user)
    expect(guard).not.toThrow()
    expect(hasAgentAdministration(user)).toBe(false)
  }
)

it.each([ROLE.ADMIN, ROLE.SUPER_ADMIN])(
  'administrator role %s can open ordinary code management',
  (role) => {
    useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role })
    expect(guard).not.toThrow()
  }
)

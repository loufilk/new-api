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
import { describe, expect, it } from 'vitest'

import type { AuthUser } from '@/stores/auth-store'

import {
  hasAgentHistory,
  hasAgentAdministration,
  isAgentWorkspaceUser,
} from '../agent-permissions'

describe('agent profile permissions', () => {
  const user: AuthUser = { id: 7, username: 'agent', role: 1 }
  it('keeps a migrated agent self-scoped without requiring an agent role', () => {
    const agent = { ...user, permissions: { agent_history: true } }
    expect(hasAgentHistory(agent)).toBe(true)
    expect(isAgentWorkspaceUser(agent)).toBe(true)
    expect(hasAgentAdministration(agent)).toBe(false)
  })
  it('does not grant administration from a profile or forged capability on a user role', () => {
    expect(
      hasAgentAdministration({ ...user, permissions: { agent_admin: true } })
    ).toBe(false)
  })
  it('requires the server capability for administrator finance access', () => {
    expect(hasAgentAdministration({ ...user, role: 10 })).toBe(false)
    expect(
      hasAgentAdministration({
        ...user,
        role: 10,
        permissions: { agent_admin: true },
      })
    ).toBe(true)
  })
  it('retains legacy agent history but never grants finance authority', () => {
    expect(hasAgentHistory({ ...user, role: 5 })).toBe(true)
    expect(hasAgentAdministration({ ...user, role: 5 })).toBe(false)
  })
  it('denies absent and unrelated identities', () => {
    expect(hasAgentHistory(null)).toBe(false)
    expect(hasAgentHistory(user)).toBe(false)
    expect(
      isAgentWorkspaceUser({
        ...user,
        role: 10,
        permissions: { agent_history: true },
      })
    ).toBe(false)
  })
})

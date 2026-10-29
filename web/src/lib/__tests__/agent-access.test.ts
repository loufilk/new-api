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

import { canAccessAgentRoute, ROLE } from '../roles'

describe('agent route isolation', () => {
  it.each([
    '/redemption-codes',
    '/redemption-codes/',
    '/agent-redemption',
    '/agent-redemption/',
  ])('allows the dedicated agent page %s', (path) =>
    expect(canAccessAgentRoute(ROLE.AGENT, path)).toBe(true)
  )
  it.each([
    '/wallet',
    '/users',
    '/channels',
    '/keys',
    '/profile',
    '/security',
    '/dashboard',
    '/redemption-codes/private',
    '/agent-redemption-other',
  ])('denies an agent access to %s', (path) =>
    expect(canAccessAgentRoute(ROLE.AGENT, path)).toBe(false)
  )
  it.each([ROLE.USER, ROLE.ADMIN, ROLE.SUPER_ADMIN])(
    'keeps other role %s route checks unchanged',
    (role) => {
      expect(canAccessAgentRoute(role, '/wallet')).toBe(true)
    }
  )
})

import { describe, expect, it } from 'vitest'

import { canAccessAgentRoute, ROLE } from '../roles'

describe('agent route isolation', () => {
  it.each(['/redemption-codes', '/redemption-codes/', '/agent-redemption', '/agent-redemption/'])(
    'allows the dedicated agent page %s',
    (path) => expect(canAccessAgentRoute(ROLE.AGENT, path)).toBe(true)
  )
  it.each(['/wallet', '/users', '/channels', '/keys', '/profile', '/security', '/dashboard', '/redemption-codes/private', '/agent-redemption-other'])(
    'denies an agent access to %s',
    (path) => expect(canAccessAgentRoute(ROLE.AGENT, path)).toBe(false)
  )
  it.each([ROLE.USER, ROLE.ADMIN, ROLE.SUPER_ADMIN])('keeps other role %s route checks unchanged', (role) => {
    expect(canAccessAgentRoute(role, '/wallet')).toBe(true)
  })
})

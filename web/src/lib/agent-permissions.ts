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
import type { AuthUser } from '@/stores/auth-store'

import { ROLE } from './roles'

export function hasAgentHistory(user: AuthUser | null | undefined): boolean {
  return user?.permissions?.agent_history === true || user?.role === ROLE.AGENT
}

export function hasAgentAdministration(
  user: AuthUser | null | undefined
): boolean {
  return Boolean(
    user && user.role >= ROLE.ADMIN && user.permissions?.agent_admin
  )
}

/** Legacy budget pages remain self-scoped after the role-to-profile migration. */
export function isAgentWorkspaceUser(
  user: AuthUser | null | undefined
): user is AuthUser {
  return Boolean(user && user.role < ROLE.ADMIN && hasAgentHistory(user))
}

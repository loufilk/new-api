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
import { useQuery } from '@tanstack/react-query'

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getAgentAccounts } from '../api'

export function useAgentAccount(enabled = true) {
  const user = useAuthStore((state) => state.auth.user)
  const query = useQuery({
    queryKey: ['agent-redemption', user?.id, user?.role, 'own-account'],
    queryFn: ({ signal }) => getAgentAccounts({ p: 1, page_size: 20 }, signal),
    enabled: enabled && user?.role === ROLE.AGENT,
  })
  return {
    ...query,
    account: query.data?.items.find(
      (account) => account.agent_user_id === user?.id
    ),
  }
}

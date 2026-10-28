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
  return { ...query, account: query.data?.items.find((account) => account.agent_user_id === user?.id) }
}

import { createFileRoute, redirect } from '@tanstack/react-router'

import { AgentRedemption } from '@/features/agent-redemption'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated/agent-redemption/')({
  beforeLoad: () => {
    const user = useAuthStore.getState().auth.user
    if (!user || (user.role !== ROLE.AGENT && user.role < ROLE.ADMIN)) throw redirect({ to: '/403' })
  },
  component: AgentRedemption,
})

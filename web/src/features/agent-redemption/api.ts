import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type { AgentAccount, AgentAllocation, AgentInvitation, AgentLedgerEntry, AgentListParams, AgentPage, InvitationPolicy } from './types'

type Response<T> = { success: boolean; data: T; message?: string }

export async function getAgentAccounts(params: AgentListParams, signal?: AbortSignal): Promise<AgentPage<AgentAccount>> {
  const response = await api.get<Response<AgentPage<AgentAccount>>>('/api/agent-redemption/accounts', { params, signal })
  return requireServerSuccess(response.data).data
}

export async function getAgentLedger(agentId: number, params: AgentListParams, signal?: AbortSignal): Promise<AgentPage<AgentLedgerEntry>> {
  const response = await api.get<Response<AgentPage<AgentLedgerEntry>>>(`/api/agent-redemption/accounts/${agentId}/ledger`, { params, signal })
  return requireServerSuccess(response.data).data
}

export async function allocateAgentQuota(payload: AgentAllocation): Promise<{ account: AgentAccount; replayed: boolean }> {
  const response = await api.post<Response<{ account: AgentAccount; replayed: boolean }>>('/api/agent-redemption/allocations', payload)
  return requireServerSuccess(response.data).data
}

export async function getAgentInvitations(params: AgentListParams, signal?: AbortSignal): Promise<AgentPage<AgentInvitation>> {
  const response = await api.get<Response<AgentPage<AgentInvitation>>>('/api/agent-redemption/invitations', { params, signal })
  return requireServerSuccess(response.data).data
}

export async function getAgentInvitationCode(signal?: AbortSignal): Promise<string> {
  const response = await api.get<Response<string>>('/api/user/aff', { signal })
  return requireServerSuccess(response.data).data
}

export async function getInvitationPolicy(signal?: AbortSignal): Promise<InvitationPolicy> {
  const response = await api.get<Response<InvitationPolicy>>('/api/agent-redemption/invitation-policy', { signal })
  return requireServerSuccess(response.data).data
}

export async function updateInvitationPolicy(payload: Pick<InvitationPolicy, 'enabled' | 'quota'>): Promise<InvitationPolicy> {
  const response = await api.put<Response<InvitationPolicy>>('/api/agent-redemption/invitation-policy', payload)
  return requireServerSuccess(response.data).data
}

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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  AgentAccount,
  AgentInvitation,
  AgentLedgerEntry,
  AgentListParams,
  AgentPage,
  InvitationPolicy,
} from './types'

type Response<T> = { success: boolean; data: T; message?: string }

export async function getAgentAccounts(
  params: AgentListParams,
  signal?: AbortSignal
): Promise<AgentPage<AgentAccount>> {
  const response = await api.get<Response<AgentPage<AgentAccount>>>(
    '/api/agent-redemption/accounts',
    { params, signal }
  )
  return requireServerSuccess(response.data).data
}

export async function getAgentLedger(
  agentId: number,
  params: AgentListParams,
  signal?: AbortSignal
): Promise<AgentPage<AgentLedgerEntry>> {
  const response = await api.get<Response<AgentPage<AgentLedgerEntry>>>(
    `/api/agent-redemption/accounts/${agentId}/ledger`,
    { params, signal }
  )
  return requireServerSuccess(response.data).data
}

export async function getAgentInvitations(
  params: AgentListParams,
  signal?: AbortSignal
): Promise<AgentPage<AgentInvitation>> {
  const response = await api.get<Response<AgentPage<AgentInvitation>>>(
    '/api/agent-redemption/invitations',
    { params, signal }
  )
  return requireServerSuccess(response.data).data
}

export async function getAgentInvitationCode(
  signal?: AbortSignal
): Promise<string> {
  const response = await api.get<Response<string>>('/api/user/aff', { signal })
  return requireServerSuccess(response.data).data
}

export async function getInvitationPolicy(
  signal?: AbortSignal
): Promise<InvitationPolicy> {
  const response = await api.get<Response<InvitationPolicy>>(
    '/api/agent-redemption/invitation-policy',
    { signal }
  )
  return requireServerSuccess(response.data).data
}

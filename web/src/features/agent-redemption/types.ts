export interface AgentAccount {
  agent_user_id: number
  username?: string
  display_name?: string
  available_quota: number
  reserved_quota: number
  spent_quota: number
  allocated_quota: number
  created_at: number
  updated_at: number
}

export interface AgentLedgerEntry {
  id: number
  agent_user_id: number
  actor_user_id: number
  kind: string
  delta: number
  reserved_delta: number
  spent_delta: number
  allocated_delta: number
  redemption_id: number
  allocated_quota_after: number
  available_quota_after: number
  reserved_quota_after: number
  spent_quota_after: number
  reason: string
  request_id: string
  created_at: number
}

export interface AgentInvitation {
  user_id: number
  inviter_id: number
  inviter_username: string
  inviter_role: number
  source: string
  bonus_quota: number
  policy_revision: number
  created_at: number
}

export interface InvitationPolicy {
  enabled: boolean
  quota: number
  updated_by: number
  updated_at: number
  revision: number
}

export interface AgentPage<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export interface AgentListParams {
  p: number
  page_size: number
  agent_user_id?: number
}

export interface AgentAllocation {
  agent_user_id: number
  delta: number
  reason: string
  request_id: string
}

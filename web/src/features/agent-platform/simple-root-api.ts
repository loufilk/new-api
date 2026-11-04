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
import { getPlatform, postPlatform } from './api'

export interface SimpleAgentRow {
  wallet_quota?: number
  cash_quota: number
  reward_quota: number
  root_quota: number
  payment_quota: number
  credit_limit_quota: number
  agent_user_id: number
  username: string
  status: string
  available_quota: number
  available_credit_quota: number
  amount_due_quota: number
  payment_available_quota: number
  payment_debt_quota: number
  overdue_quota: number
  inactive: boolean
  last_activity_at: number
}

export interface SimpleAgentPage {
  items: SimpleAgentRow[]
  total: number
  page: number
  page_size: number
}

export function getSimpleAgents(params?: { p?: number; page_size?: number }) {
  return getPlatform<SimpleAgentPage>('/simple/finance/agents', params)
}

export function getSimpleHistory(
  kind: string,
  params?: { agent_user_id?: number; p?: number; page_size?: number }
) {
  return getPlatform<SimpleHistoryPage>(
    `/simple/finance/history/${kind}`,
    params
  )
}

export interface SimpleHistoryPage {
  items: Record<string, string | number | boolean>[]
  total: number
  page: number
  page_size: number
}

export function updateSimpleStatus(payload: {
  request_id: string
  agent_user_id: number
  status: string
  reason: string
}) {
  return postPlatform('/simple/finance/status', payload)
}

export function adjustSimpleBalance(payload: {
  request_id: string
  agent_user_id: number
  delta: number
  reason: string
}) {
  return postPlatform('/simple/finance/adjust', payload)
}

export function setSimpleCreditLimit(payload: {
  request_id: string
  agent_user_id: number
  limit_quota: number
  reason: string
}) {
  return postPlatform('/simple/finance/credit-limit', payload)
}

export function repaySimpleCredit(payload: {
  request_id: string
  agent_user_id: number
  quota: number
  reason: string
}) {
  return postPlatform('/simple/finance/repayment', payload)
}

export function recordSimpleWithdrawal(payload: {
  request_id: string
  user_id: number
  referral_quota: number
  payment_quota: number
  reason: string
  evidence: string
}) {
  return postPlatform('/simple/finance/withdrawal', payload)
}

export function settleSimpleExit(payload: {
  request_id: string
  agent_user_id: number
  quota: number
  settle_all?: boolean
  source: string
  target: string
  reason: string
  evidence: string
}) {
  return postPlatform('/simple/finance/exit', payload)
}

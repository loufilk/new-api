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
export type WithdrawalNetwork = 'BSC' | 'POLYGON' | 'TRC20'
export type WithdrawalStatus =
  | 'pending'
  | 'approved'
  | 'paying'
  | 'paid'
  | 'rejected'
  | 'cancelled'

export type WithdrawalConfig = {
  revision: number
  enabled: boolean
  min_consumed_quota: number
  min_withdrawal_quota: number
  quota_per_usdt: number
  bsc_fee_bps: number
  polygon_fee_bps: number
  trc20_fee_bps: number
  instructions: string
  updated_at: number
  updated_by: number
}

export type WithdrawalInput = {
  amount_quota: number
  network: WithdrawalNetwork
  address: string
  policy_revision: number
}

export type WithdrawalAction = {
  expected_revision: number
  action: 'approve' | 'reject' | 'cancel' | 'begin_payment' | 'paid'
  note: string
  transaction_hash: string
}

export type WithdrawalEvent = {
  id: number
  actor_id: number
  action: string
  from_status: string
  to_status: WithdrawalStatus
  amount_delta: number
  frozen_delta: number
  balance_after: number
  note: string
  created_at: number
}

export type Withdrawal = WithdrawalInput & {
  id: number
  user_id: number
  quota_per_usdt: number
  gross_usdt_micro: string
  fee_usdt_micro: string
  net_usdt_micro: string
  fee_bps: number
  status: WithdrawalStatus
  revision: number
  transaction_hash: string
  review_note: string
  created_at: number
  updated_at: number
  paid_at: number
  events?: WithdrawalEvent[]
}

export type WithdrawalSummary = {
  config: WithdrawalConfig
  available_quota: number
  reserved_quota: number
  consumed_quota: number
  eligible: boolean
}

export const withdrawalStatuses: Record<WithdrawalStatus, string> = {
  pending: 'Pending review',
  approved: 'Approved for payment',
  paying: 'Manual payment in progress',
  paid: 'Paid',
  rejected: 'Rejected',
  cancelled: 'Cancelled',
}

export const withdrawalActions: Record<WithdrawalAction['action'], string> = {
  approve: 'Approve withdrawal',
  reject: 'Reject withdrawal',
  cancel: 'Cancel withdrawal',
  begin_payment: 'Start manual payment',
  paid: 'Record completed payment',
}

export function withdrawalBadgeVariant(
  status: WithdrawalStatus
): 'default' | 'destructive' | 'secondary' {
  if (status === 'paid') return 'default'
  if (status === 'rejected') return 'destructive'
  return 'secondary'
}

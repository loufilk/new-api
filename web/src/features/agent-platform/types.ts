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
import type { AgentTransactionContext } from '@/features/auth/secure-verification/types'
import type { AgentProfile } from '@/stores/auth-store'

import type { ExpansionPolicy } from './expansion-types'

export interface PlatformPage<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}
export interface PlatformSelf {
  profile: AgentProfile | null
  capabilities: {
    history: boolean
    sales: boolean
    settlement: boolean
    admin: boolean
  }
}
export interface OriginalMoney {
  currency: string
  currency_exponent: number
}
export interface BusinessResource {
  id: number
  version: number
  request_id: string
  created_at: number
  updated_at: number
  allowed_actions: string[]
}
export type OperationValue =
  | string
  | number
  | boolean
  | null
  | { [key: string]: OperationValue }
  | OperationValue[]
export type OperationPayload = Record<string, OperationValue>
export interface PreparedOperation {
  verification_context: AgentTransactionContext
  summary: {
    action: string
    agent_user_id: number
    currency: string
    currency_exponent: number
    amount_minor: string
    fee_minor: string
    net_minor: string
    quota: number
    target_label: string
    recipient_label: string
    policy_version: number
  }
  status_url: string
}
export interface OperationDraft {
  action: string
  request_id: string
  payload: OperationPayload
  endpoint: string
  resource: string
  method?: 'post' | 'patch' | 'put' | 'delete'
  review?: { label: string; before?: string; after: string }[]
}

export interface OperationStatus {
  request_id: string
  action: string
  resource_type: string
  resource_id: string
  resource_state: string
  status: 'not_observed' | 'pending' | 'completed' | 'failed'
  resource?: unknown
}

export interface PurchaseQuote extends OriginalMoney {
  price_snapshot?: string
  id: number
  agent_user_id: number
  quota: number
  paid_minor: string
  rate_bps: number
  policy_version: number
  expires_at: number
}
export interface PurchaseOrder extends PurchaseQuote {
  payer_digest?: string
  quote_id: number
  status: 'awaiting_payment' | 'evidence_submitted' | 'verified' | 'credited'
  version: number
  request_id: string
  evidence_reference: string
  verified_by: number
  reviewed_by: number
  created_at: number
  credited_at: number
  fund_batch_id: number
  allowed_actions?: string[]
}

export interface AgentPolicy extends ExpansionPolicy {
  id: number
  request_id: string
  created_by: number
  created_at: number
  reason: string
  risk_policy_approved: boolean
  purchase_refund_terms_approved: boolean
  legacy_purchase_reclassification_enabled: boolean
  purchase_enabled: boolean
  delivery_enabled: boolean
  purchase_refund_enabled: boolean
  procurement_self_use_enabled: boolean
  purchase_currency: string
  purchase_currency_exponent: number
  purchase_rate_bps: number
  purchase_quota_numerator: number
  purchase_quota_denominator: number
  purchase_minimum_quota: number
  purchase_maximum_quota: number
  quote_lifetime_seconds: number
  refund_waiting_seconds: number
  refund_fee_bps: number
  commission_enabled: boolean
  commission_cutover_at: number
  commission_currency: string
  commission_currency_exponent: number
  commission_user_rate_bps: number
  commission_agent_rate_bps: number
  commission_referral_cap_minor: string
  commission_maturity_seconds: number
  commission_reserve_bps: number
  withdrawal_enabled: boolean
  commission_conversion_enabled: boolean
  withdrawal_minimum_minor: string
  withdrawal_fee_bps: number
  withdrawal_interval_seconds: number
  withdrawal_calendar_month_limit: number
  conversion_quota_numerator: number
  conversion_quota_denominator: number
}

export interface FundBatch extends OriginalMoney {
  id: number
  source_key: string
  agent_user_id: number
  source_kind:
    | 'purchase'
    | 'commission_transfer'
    | 'legacy'
    | 'promotion'
    | 'credit'
  purchase_order_id: number
  original_quota: number
  original_paid_minor: string
  wallet_account: string
  cash_refundable: boolean
  policy_version: number
  created_at: number
  available_quota: number
  reserved_quota: number
  spent_quota: number
  refund_frozen_quota: number
  refunded_quota: number
  reclaimed_quota: number
}
export interface PurchaseRefund extends OriginalMoney {
  id: number
  request_id: string
  agent_user_id: number
  batch_id: number
  purchase_order_id: number
  quota: number
  principal_minor: string
  fee_minor: string
  net_minor: string
  policy_version: number
  state:
    | 'requested'
    | 'approved'
    | 'rejected'
    | 'payment_pending'
    | 'payment_unknown'
    | 'succeeded'
    | 'failed'
  version: number
  business_key: string
  requested_by: number
  approved_by: number
  submitted_by: number
  submitted_at?: number
  resolved_by: number
  evidence_reference: string
  external_reference: string
  created_at: number
}

export interface Delivery {
  id: number
  agent_user_id: number
  redemption_id: number
  name: string
  quota: number
  state: 'created' | 'claimed' | 'redeemed' | 'cancelled'
  version: number
  customer_user_id: number
  expires_at: number
  claimed_at: number
  redeemed_at: number
  cancellation_evidence: string
  created_at: number
  claim_code?: string
}

export interface CommissionAccount extends OriginalMoney {
  id: number
  user_id: number
  pending_minor: string
  available_minor: string
  reserved_minor: string
  withdrawal_held_minor: string
  debt_minor: string
  total_earned_minor: string
  total_reversed_minor: string
}
export interface CommissionSummary {
  accounts: CommissionAccount[]
  legacy_aff_quota: number
  allowed_actions: string[]
  disabled_reason: string
}
export interface CommissionAward extends OriginalMoney {
  id: number
  reference_id: string
  source_user_id: number
  paid_minor: string
  reward_minor: string
  reversed_minor: string
  matures_at: number
  matured: boolean
  policy_version: number
  rate_bps: number
  cap_minor: string
  created_at: number
}
export interface CommissionLedger extends OriginalMoney {
  id: number
  kind: string
  reference_type: string
  reference_id: string
  delta_pending_minor: string
  delta_available_minor: string
  delta_reserved_minor: string
  delta_held_minor: string
  delta_debt_minor: string
  after_available_minor: string
  created_at: number
}
export interface CommissionDebt {
  id: number
  award_id: number
  currency: string
  amount_minor: string
  recovered_minor: string
  created_at: number
}
export interface CommissionRecipient {
  id: string
  version: number
  label: string
  reference_masked: string
  reference_hash: string
  created_at: number
}
export interface CommissionConversion extends OriginalMoney {
  id: number
  request_id: string
  quote_id: string
  target: string
  amount_minor: string
  quota: number
  policy_version: number
  created_at: number
}
export interface CommissionWithdrawal extends OriginalMoney {
  id: number
  business_no: string
  user_id: number
  amount_minor: string
  fee_minor: string
  net_minor: string
  recipient_id: string
  recipient_version: number
  recipient_label: string
  recipient_masked: string
  status: string
  version: number
  requested_by: number
  approved_by: number
  submitted_by?: number
  policy_version: number
  evidence_reference: string
  reason: string
  created_at: number
  allowed_actions: string[]
}

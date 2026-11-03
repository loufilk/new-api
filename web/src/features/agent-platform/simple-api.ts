/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import type { Redemption } from '@/features/redemption-codes/types'

import { getPlatform, postPlatform } from './api'

export interface SimpleAgentBalance {
  agent_user_id: number
  status: string
  cash_quota: number
  reward_quota: number
  root_quota: number
  credit_limit_quota?: number
  credit_reserved_quota: number
  credit_outstanding_quota: number
  payment_pending_quota: number
  payment_available_quota: number
  payment_total_quota: number
  payment_debt_quota: number
  available_quota: number
  available_credit_quota: number
  amount_due_quota: number
  reserved_quota: number
  overdue_quota?: number
}

export interface SimpleCodeRecord {
  id: number
  agent_user_id: number
  name: string
  key?: string
  masked_key: string
  quota: number
  mode: 'direct' | 'credit' | 'payment'
  status: number
  expired_at: number
  redeemed_at: number
  created_at: number
  version: number
  cost_quota: number
}

export interface SimpleCodePage {
  items: SimpleCodeRecord[]
  total: number
  page: number
  page_size: number
}

export interface SimpleCodeRequest {
  request_id: string
  name: string
  quota: number
  mode: 'direct' | 'credit'
  expires_at: number
}

export interface SimpleRechargeResponse {
  url: string
  data: Record<string, unknown>
  quota: number
  pay_yuan: number
}

export interface SimplePaymentOrder {
  id: number
  agent_user_id: number
  public_id: string
  name: string
  quota: number
  sale_price_fen: number
  cost_price_fen: number
  fee_fen: number
  profit_quota: number
  status: string
  customer_url?: string
  expires_at: number
  paid_at: number
  delivered_at: number
  created_at: number
  matures_at: number
  profit_matured: boolean
  redemption_id: number
  redemption_key?: string
  refund_reason?: string
  refund_evidence?: string
  refunded_by?: number
  refunded_at?: number
}

export interface SimplePaymentQuote {
  quota: number
  sale_price_fen: number
  cost_price_fen: number
  fee_fen: number
  profit_quota: number
  minimum_sale_price_fen: number
  fee_version: number
  price_valid: boolean
}

export interface SimplePaymentRequest {
  request_id: string
  name: string
  quota: number
  sale_price_fen: number
  payment_method: string
  fee_version?: number
}

export interface SimplePaymentOrderPage {
  items: SimplePaymentOrder[]
  total: number
  page: number
  page_size: number
}

export interface SimplePaymentMethod {
  payment_method: string
  fee_bps: number
  enabled: boolean
  version: number
}

export async function getSimpleAgentBalance(
  signal?: AbortSignal
): Promise<SimpleAgentBalance> {
  return getPlatform<SimpleAgentBalance>('/simple/balance', undefined, signal)
}

export async function getSimpleAgentCodes(params?: {
  agent_user_id?: number
  mode?: 'direct' | 'credit' | 'payment'
  p?: number
  page_size?: number
}): Promise<SimpleCodePage> {
  return getPlatform<SimpleCodePage>('/simple/codes', params)
}

export async function createSimpleCode(
  payload: SimpleCodeRequest
): Promise<Redemption> {
  return postPlatform<Redemption>('/simple/codes', payload)
}

export async function requestSimpleRecharge(payload: {
  request_id: string
  amount: string
  kind: 'recharge' | 'repayment'
  payment_method: string
}): Promise<SimpleRechargeResponse> {
  return postPlatform<SimpleRechargeResponse>('/simple/recharge', payload)
}

export async function cancelSimpleCode(
  codeId: number,
  payload: { request_id: string; version: number; reason: string }
): Promise<Redemption> {
  return postPlatform<Redemption>(`/simple/codes/${codeId}/cancel`, payload)
}

export async function createSimplePayment(
  payload: SimplePaymentRequest
): Promise<{
  order: SimplePaymentOrder
  payment_url: string
  payment_params: Record<string, unknown>
}> {
  return postPlatform('/simple/payment', payload)
}

export function quoteSimplePayment(
  payload: Omit<SimplePaymentRequest, 'request_id'>
): Promise<SimplePaymentQuote> {
  return postPlatform('/simple/payment/quote', payload)
}

export async function getSimplePaymentOrders(params?: {
  agent_user_id?: number
  p?: number
  page_size?: number
}): Promise<SimplePaymentOrderPage> {
  return getPlatform<SimplePaymentOrderPage>('/simple/payment-orders', params)
}

export async function getSimplePaymentMethods(
  purpose: 'payment' | 'recharge' | 'repayment' = 'payment'
): Promise<SimplePaymentMethod[]> {
  return getPlatform<SimplePaymentMethod[]>('/simple/payment-methods', {
    purpose,
  })
}

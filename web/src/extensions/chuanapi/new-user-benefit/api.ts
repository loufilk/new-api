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

import type { ApiResponse } from '../wallet/types'
import type {
  BenefitCheckout,
  BenefitConfigUpdate,
  BenefitPaymentRequest,
  NewUserBenefitConfig,
  NewUserBenefitInfo,
} from './types'

function requireData<T>(response: ApiResponse<T>): T {
  const result = requireServerSuccess(response)
  if (result.success !== true || !result.data) {
    throw new Error('New user recharge benefit is unavailable.')
  }
  return result.data
}

export async function getNewUserBenefit(signal?: AbortSignal) {
  const response = await api.get<ApiResponse<NewUserBenefitInfo>>(
    '/api/user/new-user-benefit',
    { signal }
  )
  return requireData(response.data)
}

export async function requestBenefitPayment(
  request: BenefitPaymentRequest,
  idempotencyKey: string,
  turnstile: string
) {
  const response = await api.post<ApiResponse<BenefitCheckout>>(
    '/api/user/new-user-benefit/pay',
    request,
    {
      headers: { 'Idempotency-Key': idempotencyKey },
      params: { turnstile },
    }
  )
  return requireData(response.data)
}

export async function getBenefitConfig(signal?: AbortSignal) {
  const response = await api.get<ApiResponse<NewUserBenefitConfig>>(
    '/api/option/new-user-benefit',
    { signal }
  )
  return requireData(response.data)
}

export async function saveBenefitConfig(config: BenefitConfigUpdate) {
  const response = await api.put<ApiResponse<NewUserBenefitConfig>>(
    '/api/option/new-user-benefit',
    {
      enabled: config.enabled,
      require_verified_email: config.require_verified_email,
      require_turnstile: config.require_turnstile,
      network_limit_enabled: config.network_limit_enabled,
      payment_identity_check_enabled: config.payment_identity_check_enabled,
      pay_amount_cny: config.pay_amount_cny,
      credit_amount_cny: config.credit_amount_cny,
      max_total_orders: config.max_total_orders,
      max_orders_per_24h: config.max_orders_per_24h,
      max_orders_per_network_24h: config.max_orders_per_network_24h,
      total_bonus_budget_cny: config.total_bonus_budget_cny,
      version: config.version,
    }
  )
  return requireData(response.data)
}

export async function recheckBenefitPaymentIdentity(): Promise<NewUserBenefitInfo> {
  const response = await api.post<ApiResponse<NewUserBenefitInfo>>(
    '/api/user/new-user-benefit/recheck'
  )
  return requireData(response.data)
}

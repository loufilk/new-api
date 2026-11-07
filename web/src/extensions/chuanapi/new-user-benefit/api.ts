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
  idempotencyKey: string
) {
  const response = await api.post<ApiResponse<BenefitCheckout>>(
    '/api/user/new-user-benefit/pay',
    request,
    { headers: { 'Idempotency-Key': idempotencyKey } }
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

export async function saveBenefitConfig(config: NewUserBenefitConfig) {
  const response = await api.put<ApiResponse<NewUserBenefitConfig>>(
    '/api/option/new-user-benefit',
    config
  )
  return requireData(response.data)
}

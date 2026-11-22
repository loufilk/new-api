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
import { createServerError } from '@/lib/server-error-message'

import type {
  Redemption,
  RedemptionFormData,
  ApiResponse,
  GetRedemptionsParams,
  GetRedemptionsResponse,
  SearchRedemptionsParams,
} from './types'

export async function getRedemptions(
  params: GetRedemptionsParams = {}
): Promise<GetRedemptionsResponse> {
  const { p = 1, page_size = 10 } = params
  const res = await api.get(`/api/redemption/?p=${p}&page_size=${page_size}`)
  return res.data
}

export async function searchRedemptions(
  params: SearchRedemptionsParams
): Promise<GetRedemptionsResponse> {
  const { keyword = '', status = '', p = 1, page_size = 10 } = params
  const queryParams = new URLSearchParams({
    keyword,
    p: String(p),
    page_size: String(page_size),
  })
  if (status) queryParams.set('status', status)
  const res = await api.get(`/api/redemption/search?${queryParams.toString()}`)
  return res.data
}

export async function getRedemption(
  id: number
): Promise<ApiResponse<Redemption>> {
  const res = await api.get(`/api/redemption/${id}`)
  return res.data
}

export async function createRedemption(
  payload: RedemptionFormData
): Promise<string[]> {
  const { data } = await api.post<ApiResponse<string[]>>(
    '/api/redemption/',
    payload
  )
  if (!data.success) {
    throw createServerError(data, 'Failed to create redemption code')
  }
  return data.data ?? []
}

export async function updateRedemption(
  payload: RedemptionFormData
): Promise<void> {
  const { data } = await api.put<ApiResponse>('/api/redemption/', payload)
  if (!data.success) {
    throw createServerError(data, 'Failed to update redemption code')
  }
}

export async function updateRedemptionStatus(payload: {
  id: number
  status: number
}): Promise<void> {
  const { data } = await api.put<ApiResponse>(
    '/api/redemption/?status_only=true',
    payload
  )
  if (!data.success) {
    throw createServerError(data, 'Failed to update redemption code status')
  }
}

export async function deleteRedemption(id: number): Promise<void> {
  const { data } = await api.delete<ApiResponse>(`/api/redemption/${id}`)
  if (!data.success) {
    throw createServerError(data, 'Failed to delete redemption code')
  }
}

export async function deleteRedemptions(ids: number[]): Promise<number> {
  const { data } = await api.post<ApiResponse<number>>(
    '/api/redemption/batch',
    { ids }
  )
  if (!data.success) {
    throw createServerError(data, 'Failed to delete redemption code')
  }
  return data.data ?? 0
}

export async function deleteInvalidRedemptions(): Promise<number> {
  const { data } = await api.delete<ApiResponse<number>>(
    '/api/redemption/invalid'
  )
  if (!data.success) {
    throw createServerError(data, 'Failed to delete invalid redemption codes')
  }
  return data.data ?? 0
}

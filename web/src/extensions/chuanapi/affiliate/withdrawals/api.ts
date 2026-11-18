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
  Withdrawal,
  WithdrawalAction,
  WithdrawalConfig,
  WithdrawalInput,
  WithdrawalSummary,
} from './types'

type Response<T> = { success: boolean; message: string; data: T }
const base = '/api/referral-withdrawals'
const adminBase = '/api/admin/referral-withdrawals'

export async function getWithdrawalSummary(): Promise<WithdrawalSummary> {
  const result = await api.get<Response<WithdrawalSummary>>(`${base}/summary`)
  return requireServerSuccess(result.data).data
}

export async function listWithdrawals(
  admin: boolean,
  status: string,
  page: number
): Promise<{ items: Withdrawal[]; total: number }> {
  const result = await api.get<
    Response<{ items: Withdrawal[]; total: number }>
  >(admin ? adminBase : base, {
    params: { status: status || undefined, p: page, page_size: 20 },
  })
  return requireServerSuccess(result.data).data
}

export async function getWithdrawal(
  admin: boolean,
  id: number
): Promise<Withdrawal> {
  const result = await api.get<Response<Withdrawal>>(
    `${admin ? adminBase : base}/${id}`
  )
  return requireServerSuccess(result.data).data
}

export async function quoteWithdrawal(
  input: WithdrawalInput
): Promise<Withdrawal> {
  const result = await api.post<Response<Withdrawal>>(`${base}/quote`, input)
  return requireServerSuccess(result.data).data
}

export async function createWithdrawal(
  input: WithdrawalInput,
  key: string,
  proof: string
): Promise<Withdrawal> {
  const result = await api.post<Response<Withdrawal>>(base, input, {
    headers: { 'Idempotency-Key': key, 'X-Security-Proof': proof },
  })
  return requireServerSuccess(result.data).data
}

export async function actOnWithdrawal(
  id: number,
  input: WithdrawalAction,
  key: string,
  proof?: string
): Promise<Withdrawal> {
  const path =
    input.action === 'cancel'
      ? `${base}/${id}/cancel`
      : `${adminBase}/${id}/${['paid', 'begin_payment'].includes(input.action) ? 'pay' : 'review'}`
  const result = await api.post<Response<Withdrawal>>(path, input, {
    headers: {
      'Idempotency-Key': key,
      ...(proof ? { 'X-Security-Proof': proof } : {}),
    },
  })
  return requireServerSuccess(result.data).data
}

export async function getWithdrawalConfig(): Promise<WithdrawalConfig> {
  const result = await api.get<Response<WithdrawalConfig>>(
    `${adminBase}/config`
  )
  return requireServerSuccess(result.data).data
}

export async function saveWithdrawalConfig(
  input: WithdrawalConfig
): Promise<WithdrawalConfig> {
  const result = await api.put<Response<WithdrawalConfig>>(
    `${adminBase}/config`,
    input
  )
  return requireServerSuccess(result.data).data
}

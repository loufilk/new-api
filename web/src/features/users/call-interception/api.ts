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

import { callInterceptionConfigSchema } from './lib/schema'
import type {
  CallInterceptionUpdate,
  InterceptionLogFilters,
  InterceptionLogKind,
  InterceptionLogPage,
  UserCallInterception,
} from './types'

export const callInterceptionKey = (userId: number) =>
  ['user-call-interception', userId] as const

export async function getCallInterception(
  userId: number,
  signal?: AbortSignal
): Promise<UserCallInterception> {
  const response = await api.get(`/api/user/${userId}/call-interception`, {
    signal,
  })
  return callInterceptionConfigSchema.parse(
    requireServerSuccess(response.data).data
  )
}

export async function updateCallInterception(
  userId: number,
  payload: CallInterceptionUpdate
): Promise<UserCallInterception> {
  const response = await api.put(
    `/api/user/${userId}/call-interception`,
    payload
  )
  return callInterceptionConfigSchema.parse(
    requireServerSuccess(response.data).data
  )
}

export async function getCallInterceptionLogs(
  userId: number,
  kind: InterceptionLogKind,
  filters: InterceptionLogFilters,
  signal?: AbortSignal
): Promise<InterceptionLogPage> {
  const response = await api.get(
    `/api/user/${userId}/call-interception/${kind}`,
    { params: filters, signal }
  )
  return requireServerSuccess(response.data).data
}

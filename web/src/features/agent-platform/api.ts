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

type Response<T> = { success: boolean; data: T; message?: string }
export const PLATFORM_ROOT = '/api/agent-platform'

export async function getPlatform<T>(
  path: string,
  params?: Record<string, string | number | undefined>,
  signal?: AbortSignal
): Promise<T> {
  const response = await api.get<Response<T>>(`${PLATFORM_ROOT}${path}`, {
    params,
    signal,
  })
  return requireServerSuccess(response.data).data
}

export async function postPlatform<T>(
  path: string,
  payload: unknown,
  proof?: string,
  method: 'post' | 'patch' | 'put' | 'delete' = 'post'
): Promise<T> {
  const response = await api.request<Response<T>>({
    method,
    url: `${PLATFORM_ROOT}${path}`,
    data: payload,
    ...(proof
      ? {
          headers: { 'X-Security-Proof': proof },
          skipAuthRefresh: true,
          singleUseAuthorization: true,
        }
      : {}),
  })
  return requireServerSuccess(response.data).data
}

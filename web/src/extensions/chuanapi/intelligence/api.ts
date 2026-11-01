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
  TestCatalogue,
  TestDetail,
  TestEstimate,
  TestHistory,
  TestRequest,
  TestRun,
  TestSelection,
} from './types'

type Response<T> = { success: boolean; data: T }
const endpoint = '/api/user/model-tests'
export async function getTestHistory(
  signal?: AbortSignal
): Promise<TestHistory> {
  return requireServerSuccess(
    (await api.get<Response<TestHistory>>(endpoint, { signal })).data
  ).data
}
export async function getTestCatalogue(
  signal?: AbortSignal
): Promise<TestCatalogue> {
  return requireServerSuccess(
    (
      await api.get<Response<TestCatalogue>>('/api/user/playground/bootstrap', {
        signal,
      })
    ).data
  ).data
}
export async function estimateTest(
  selection: TestSelection
): Promise<TestEstimate> {
  return requireServerSuccess(
    (await api.post<Response<TestEstimate>>(`${endpoint}/estimate`, selection))
      .data
  ).data
}
export async function startTest(request: TestRequest): Promise<TestRun> {
  // Longer than the server's 90-second deadline; transport timeout is not cancellation.
  return requireServerSuccess(
    (
      await api.post<Response<TestRun>>(`${endpoint}/runs`, request, {
        timeout: 110_000,
        skipAuthRefresh: true,
      })
    ).data
  ).data
}
export async function cancelTest(id: string): Promise<TestRun> {
  return requireServerSuccess(
    (
      await api.post<Response<TestRun>>(
        `${endpoint}/runs/${encodeURIComponent(id)}/cancel`
      )
    ).data
  ).data
}
export async function getTestDetail(
  id: string,
  signal?: AbortSignal
): Promise<TestDetail> {
  return requireServerSuccess(
    (
      await api.get<Response<TestDetail>>(
        `${endpoint}/runs/${encodeURIComponent(id)}`,
        { signal }
      )
    ).data
  ).data
}

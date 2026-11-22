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
  SiteTests,
  SiteRun,
  PublicTestRun,
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
export async function startTest(
  request: TestRequest,
  timeoutSeconds: number
): Promise<TestRun> {
  // Allow the confirmed round budget plus time to persist and return its result.
  return requireServerSuccess(
    (
      await api.post<Response<TestRun>>(`${endpoint}/runs`, request, {
        timeout: timeoutSeconds * 1000 + 20_000,
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

export async function getSiteTests(signal?: AbortSignal): Promise<SiteTests> {
  const response = requireServerSuccess(
    (
      await api.get<
        Response<
          Omit<SiteTests, 'items' | 'user_items'> & {
            items: SiteRun[]
            user_items?: PublicTestRun[]
          }
        >
      >(`${endpoint}/site`, {
        signal,
      })
    ).data
  ).data
  return {
    ...response,
    enabled: response.enabled === true,
    user_items: (response.user_items ?? []).map((run) => ({
      id: run.id,
      model: run.model,
      group: run.group,
      benchmark_id: run.benchmark_id,
      status: run.status,
      assessment: run.assessment,
      fingerprint_result: run.fingerprint_result,
      created_at: run.created_at,
      completed_at: run.completed_at,
      source: 'user' as const,
      visibility: 'public' as const,
      max_output_tokens: 0,
      estimated_quota: null,
      request_id: '',
      error_code: '',
      cancel_requested: false,
    })),
    items: response.items.map((run) => ({
      ...run,
      source: 'site' as const,
      visibility: 'public' as const,
      status: run.status === 'succeeded' ? ('completed' as const) : run.status,
      max_output_tokens: 0,
      estimated_quota: null,
      request_id: '',
      error_code:
        run.status === 'succeeded' || run.status === 'completed'
          ? ''
          : run.assessment,
      cancel_requested: false,
    })),
  }
}

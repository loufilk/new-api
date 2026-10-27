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
import * as z from 'zod'

import { requireServerSuccess } from '@/lib/server-error-message'

const performanceSummarySchema = z.object({
  success_rate: z.number().min(0).max(100),
  avg_latency_ms: z.number().nonnegative(),
  avg_tps: z.number().nonnegative(),
})

const dashboardPerformanceSchema = z.object({
  success: z.literal(true),
  data: z.object({
    summary: performanceSummarySchema.nullish(),
    models: z.array(
      performanceSummarySchema.extend({ model_name: z.string() })
    ),
  }),
})

export type DashboardPerformanceSummary = z.output<
  typeof dashboardPerformanceSchema
>['data']

export class InvalidPerformanceSummaryError extends Error {
  readonly code = 'INVALID_PERFORMANCE_SUMMARY'

  constructor() {
    // Never retain the malformed response or schema issues: either may contain
    // private server details. Consumers can identify this stable error kind.
    super('Invalid performance summary response')
    this.name = 'InvalidPerformanceSummaryError'
  }
}

/** Validate both fetched and already-cached data without changing the raw API. */
export function selectDashboardPerformanceSummary(
  response: unknown
): DashboardPerformanceSummary {
  requireServerSuccess(response)
  const parsed = dashboardPerformanceSchema.safeParse(response)
  if (!parsed.success) throw new InvalidPerformanceSummaryError()
  return parsed.data.data
}

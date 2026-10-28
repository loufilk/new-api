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
import { useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'

import { getPerfMetricsSummary } from '@/features/performance-metrics/api'
import { selectDashboardPerformanceSummary } from '@/features/performance-metrics/lib/summary-response'
import { requireServerSuccess } from '@/lib/server-error-message'

const PERFORMANCE_WINDOW_HOURS = 24

/** Optional admin panels share raw cached responses, not a transformed shape. */
export function usePerformanceSummary() {
  const [lastError, setLastError] = useState<Error | null>(null)
  const query = useQuery({
    queryKey: ['perf-metrics-summary', PERFORMANCE_WINDOW_HOURS],
    queryFn: async () =>
      requireServerSuccess(
        await getPerfMetricsSummary(PERFORMANCE_WINDOW_HOURS)
      ),
    // select also validates fresh cache entries, which can bypass queryFn.
    // The raw query cache/API contract remains unchanged for other consumers.
    select: selectDashboardPerformanceSummary,
    staleTime: 60 * 1000,
    retry: false,
    meta: {
      errorToast: false,
      redirectOnInternalServerError: false,
    },
  })

  useEffect(() => {
    if (query.isError) setLastError(query.error)
    else if (query.isSuccess) setLastError(null)
  }, [query.error, query.isError, query.isSuccess])

  return {
    ...query,
    // Keep the error and disabled retry visible while an initial failure is
    // being retried; React Query resets error to null when that fetch begins.
    panelError: query.error ?? (query.isPending ? lastError : null),
  }
}

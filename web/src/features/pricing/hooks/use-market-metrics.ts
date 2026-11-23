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

import {
  getPerfMetrics,
  getPerfMetricsSummary,
} from '@/features/performance-metrics/api'
import type { PerformanceMetricsData } from '@/features/performance-metrics/types'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

function useMarketIdentity() {
  return useAuthStore(({ auth }) => {
    if (auth.bootstrapState === 'checking') return null
    return auth.user
      ? `user:${auth.user.id}:${auth.session?.sid ?? ''}`
      : 'public'
  })
}

const HOURS = 24

export function useMarketSummary(models: string[]) {
  const scope = useMarketIdentity()
  return useQuery({
    queryKey: ['market-perf-summary', scope, HOURS, models],
    queryFn: async () =>
      requireServerSuccess(await getPerfMetricsSummary(HOURS, models)),
    enabled: scope !== null && models.length > 0,
    staleTime: 60_000,
    retry: false,
  })
}

// Visitors can read aggregate health. Never fetch group-level rows when the
// session is unresolved or anonymous, including for a direct model URL.
export function useMarketModelMetrics(name: string) {
  const scope = useMarketIdentity()
  return useQuery({
    queryKey: ['market-perf-model', scope, HOURS, name],
    queryFn: async (): Promise<PerformanceMetricsData> => {
      if (scope === 'public') {
        const response = requireServerSuccess(
          await getPerfMetricsSummary(HOURS, [name])
        )
        return {
          success: true,
          data: {
            model_name: name,
            groups: [],
            summary:
              response.data.models?.find((item) => item.model_name === name) ??
              null,
          },
        }
      }
      return requireServerSuccess(await getPerfMetrics(name, HOURS))
    },
    enabled: scope !== null && !!name,
    staleTime: 60_000,
    retry: false,
  })
}

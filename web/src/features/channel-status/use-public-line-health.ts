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
import { z } from 'zod'

const measurement = z.object({
  success_rate: z.number().min(0).max(100).nullable(),
  average_latency_ms: z.number().nonnegative().nullable(),
  cache_hit_rate: z.number().min(0).max(100).nullable(),
})

const publicLineEnvelope = z.object({
  success: z.literal(true),
  data: z.object({
    window_start: z.number().int(),
    window_end: z.number().int(),
    lines: z
      .array(
        measurement.extend({
          id: z.string().min(1),
          label: z.string().min(1),
          representative_model: z.string().min(1),
          vendor: z.string().optional(),
          ratio: z.number().nonnegative(),
          sort_order: z.number(),
          buckets: z
            .array(measurement.extend({ bucket_start: z.number().int() }))
            .max(24),
        })
      )
      .max(200),
  }),
})

export function usePublicLineHealth(refresh: number) {
  return useQuery({
    queryKey: ['channel-health', 'published-lines'],
    queryFn: async ({ signal }) => {
      const response = await fetch('/api/uptime/lines', {
        signal,
        credentials: 'omit',
        cache: 'no-store',
      })
      if (!response.ok) throw new Error('Public line metrics unavailable')
      return publicLineEnvelope.parse(await response.json()).data
    },
    retry: false,
    gcTime: 0,
    refetchOnMount: 'always',
    refetchInterval: refresh || false,
    refetchIntervalInBackground: false,
    meta: { errorToast: false, redirectOnInternalServerError: false },
  })
}

export type PublicLine = z.infer<
  typeof publicLineEnvelope
>['data']['lines'][number]
export type Measurement = z.infer<typeof measurement>

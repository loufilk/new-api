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
import { CanceledError } from 'axios'
import { z } from 'zod'

import { api } from '@/lib/api'
import { getAuthSessionEpoch } from '@/lib/auth-session'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

const count = z.number().int().nonnegative()
const unixTime = count.max(8_640_000_000_000)
const samples = z.object({
  request_count: count,
  success_count: count,
  success_rate: z.number().min(0).max(100).nullable(),
  ttft_sample_count: count,
  average_ttft_ms: z.number().nonnegative().nullable(),
})

const groupHealth = samples.extend({
  group: z.string(),
  available: z.boolean(),
  metrics_available: z.boolean(),
  window_start: unixTime,
  window_end: unixTime,
  observed_at: unixTime,
  latest_bucket_start: unixTime.nullable(),
  bucket_seconds: z.literal(3600),
  flush_interval_seconds: count,
  buckets: z.array(samples.extend({ bucket_start: unixTime })).length(24),
})

export type KeyGroupHealth = z.infer<typeof groupHealth>

export function useKeyGroupHealth(groupId: string) {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const epoch = getAuthSessionEpoch()
  const enabled = Boolean(
    userId && sessionId && groupId.trim() && groupId !== 'auto'
  )
  const result = useQuery({
    queryKey: ['api-key-group-health', userId, sessionId, epoch, groupId],
    enabled,
    gcTime: 0,
    staleTime: 0,
    refetchOnMount: 'always',
    retry: false,
    meta: { errorToast: false, redirectOnInternalServerError: false },
    queryFn: async ({ signal }) => {
      const response = await api.get('/api/token/group-health', {
        params: { group: groupId },
        signal,
        authSessionEpoch: epoch,
      })
      if (signal.aborted || epoch !== getAuthSessionEpoch()) {
        throw new CanceledError('Group health request superseded')
      }

      const envelope = requireServerSuccess(response.data)
      const data = groupHealth.parse(envelope?.data)
      if (
        envelope?.success !== true ||
        data.group !== groupId ||
        data.window_start > data.window_end ||
        data.window_end > data.observed_at ||
        data.buckets.some(
          (bucket, index) =>
            bucket.bucket_start !==
              data.window_start + index * data.bucket_seconds ||
            bucket.bucket_start > data.window_end
        )
      ) {
        throw new Error('Invalid group health response')
      }
      return data
    },
  })
  return { enabled, result }
}

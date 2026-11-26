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
import { CanceledError, isAxiosError, isCancel } from 'axios'
import { z } from 'zod'

import { groupPlatform } from '@/features/pricing/lib/market-groups'
import { api } from '@/lib/api'
import { getAuthSessionEpoch } from '@/lib/auth-session'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

const clock = z.number().int().nonnegative().max(8_640_000_000_000)
const measurement = z.object({
  success_rate: z.number().min(0).max(100).nullable(),
  average_ttft_ms: z.number().nonnegative().nullable(),
})
const groupHealth = measurement.extend({
  group: z.string(),
  available: z.boolean(),
  metrics_available: z.boolean(),
  window_start: clock,
  observed_at: clock,
  bucket_seconds: z.literal(3600),
  buckets: z.array(measurement.extend({ bucket_start: clock })).length(24),
})
const groupCatalog = z.object({
  ratio: z.number().nonnegative(),
  desc: z.string(),
  catalog: z
    .object({
      label: z.string().optional(),
      description: z.string().optional(),
      platform: z.string().optional(),
      icon: z.string().optional(),
      exclusive: z.boolean().optional(),
    })
    .optional(),
})

export type ChannelGroupHealth = z.infer<typeof groupHealth>
export type ChannelStatusGroup = {
  name: string
  label: string
  description: string
  ratio: number
  catalog: z.infer<typeof groupCatalog>['catalog']
  platform: ReturnType<typeof groupPlatform>
  health: ChannelGroupHealth
}

const view = 'channel-status'

function assertCurrent(signal: AbortSignal, epoch: number) {
  if (signal.aborted || epoch !== getAuthSessionEpoch()) {
    throw new CanceledError('Channel status request superseded')
  }
}

async function loadChannelStatus(signal: AbortSignal, epoch: number) {
  const response = await api.get('/api/user/self/groups', {
    params: { view },
    signal,
    authSessionEpoch: epoch,
  })
  assertCurrent(signal, epoch)
  const groups = Object.entries(
    z
      .record(z.string(), z.unknown())
      .parse(requireServerSuccess(response.data)?.data)
  ).flatMap(([name, value]) => {
    if (!name.trim() || name === 'auto' || name === 'all') return []
    const result = groupCatalog.safeParse(value)
    return result.success && !result.data.catalog?.exclusive
      ? [[name, result.data] as const]
      : []
  })

  const measured: ChannelStatusGroup[] = []
  let incomplete = false
  let position = 0
  // Preserve one-group authorization without an unbounded page fan-out.
  await Promise.all(
    Array.from({ length: Math.min(4, groups.length) }, async () => {
      for (;;) {
        assertCurrent(signal, epoch)
        const entry = groups[position++]
        if (!entry) return
        const [name, { desc, ratio, catalog }] = entry
        try {
          const result = await api.get('/api/token/group-health', {
            params: { group: name, view },
            signal,
            authSessionEpoch: epoch,
          })
          assertCurrent(signal, epoch)
          const health = groupHealth.parse(
            requireServerSuccess(result.data)?.data
          )
          if (
            health.group !== name ||
            health.window_start > health.observed_at ||
            health.buckets.some(
              (bucket, index) =>
                bucket.bucket_start !==
                  health.window_start + index * health.bucket_seconds ||
                bucket.bucket_start > health.observed_at
            )
          ) {
            throw new Error('Invalid channel status response')
          }
          measured.push({
            name,
            ratio,
            catalog,
            health,
            platform: groupPlatform(catalog),
            label: catalog?.label?.trim() || name,
            description: catalog?.description?.trim() || desc,
          })
        } catch (error) {
          assertCurrent(signal, epoch)
          if (
            isCancel(error) ||
            (isAxiosError(error) && error.response?.status === 401)
          ) {
            throw error
          }
          // A fresh failed authorization must not publish catalog metadata.
          incomplete = true
        }
      }
    })
  )
  assertCurrent(signal, epoch)
  return { groups: measured, incomplete }
}

export const CHANNEL_STATUS_REFRESH_MS = 60_000

export function useChannelStatus(autoRefresh: boolean) {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const epoch = getAuthSessionEpoch()
  return useQuery({
    queryKey: ['channel-status', userId, sessionId, epoch],
    enabled: Boolean(userId && sessionId),
    gcTime: 0,
    refetchOnMount: 'always',
    refetchOnWindowFocus: false,
    refetchInterval: autoRefresh ? CHANNEL_STATUS_REFRESH_MS : false,
    refetchIntervalInBackground: false,
    retry: false,
    meta: { errorToast: false, redirectOnInternalServerError: false },
    queryFn: ({ signal }) => loadChannelStatus(signal, epoch),
  })
}

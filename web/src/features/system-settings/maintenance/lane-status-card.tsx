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
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

const laneStatusSchema = z.object({
  success: z.literal(true),
  data: z
    .object({
      build_profile: z.enum(['ordinary', 'enterprise_lane_lab']),
      gateway_enabled: z.boolean(),
      runtime_capable: z.boolean(),
      observation_scope: z.literal('current_instance'),
    })
    .refine(
      (data) =>
        data.build_profile === 'enterprise_lane_lab' || !data.gateway_enabled
    ),
})

export function LaneStatusCard() {
  const superAdminId = useAuthStore((state) =>
    state.auth.user?.role === ROLE.SUPER_ADMIN ? state.auth.user.id : null
  )
  if (superAdminId == null) return null
  return <LaneStatusSnapshot key={superAdminId} actorId={superAdminId} />
}

function LaneStatusSnapshot(props: { actorId: number }) {
  const { t } = useTranslation()
  const titleId = useId()
  const query = useQuery({
    queryKey: ['operations', 'lane-status', props.actorId],
    queryFn: async ({ signal }) => {
      const response = await api.get<unknown>('/api/performance/lane_status', {
        signal,
        skipErrorHandler: true,
      })
      return laneStatusSchema.parse(response.data).data
    },
    retry: false,
    gcTime: 0,
    refetchOnMount: 'always',
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    meta: { errorToast: false, redirectOnInternalServerError: false },
  })

  // This is an instance observation, not a fleet-wide assertion. Hide old data
  // while refreshing and after failure, including responses from old binaries.
  const status =
    query.isFetchedAfterMount && !query.isFetching && !query.isError
      ? query.data
      : undefined
  const loading = query.isFetching || query.isPending

  return (
    <Card className='shrink-0' role='region' aria-labelledby={titleId}>
      <CardHeader>
        <CardTitle id={titleId}>
          <h4>{t('laneStatus.title')}</h4>
        </CardTitle>
        <CardDescription>{t('laneStatus.scope')}</CardDescription>
        <CardAction>
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={loading}
            onClick={() => void query.refetch()}
          >
            {t('Refresh')}
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        <div role='status' aria-busy={loading}>
          {loading && (
            <LoadingState inline size='sm' message={t('Loading...')} />
          )}
          {!loading && !status && (
            <p className='text-muted-foreground'>{t('laneStatus.unknown')}</p>
          )}
          {status && (
            <dl className='grid gap-3 sm:grid-cols-3'>
              <div>
                <dt className='text-muted-foreground mb-1'>
                  {t('laneStatus.build')}
                </dt>
                <dd>
                  {t(
                    status.build_profile === 'ordinary'
                      ? 'laneStatus.ordinary'
                      : 'laneStatus.lab'
                  )}
                </dd>
              </div>
              <div>
                <dt className='text-muted-foreground mb-1'>
                  {t('laneStatus.labGateway')}
                </dt>
                <dd>
                  <StatusBadge
                    copyable={false}
                    variant={status.gateway_enabled ? 'info' : 'neutral'}
                    label={
                      status.gateway_enabled ? t('Enabled') : t('Disabled')
                    }
                  />
                </dd>
              </div>
              <div>
                <dt className='text-muted-foreground mb-1'>
                  {t('laneStatus.runtimeGate')}
                </dt>
                <dd>
                  <StatusBadge
                    copyable={false}
                    variant={status.runtime_capable ? 'info' : 'warning'}
                    label={t(
                      status.runtime_capable
                        ? 'laneStatus.gatePassed'
                        : 'laneStatus.gateClosed'
                    )}
                  />
                </dd>
              </div>
            </dl>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

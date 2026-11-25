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
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { StaticDataTable } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { MarketRateChangeLine } from '@/features/pricing/components/group-market-activity'
import { validMarketTime } from '@/features/pricing/lib/group-market-activity'
import type { MarketRateChange } from '@/features/pricing/types'
import { api } from '@/lib/api'
import { formatTimestampToDate } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

const unixSeconds = z.number().refine(validMarketTime)
const ratio = z.number().nonnegative()
const snapshot = z.object({
  groups: z.record(
    z.string(),
    z.object({
      first_configured_at: unixSeconds.optional(),
      is_new: z.boolean().optional(),
      rate_changes: z
        .array(
          z.object({
            old_ratio: ratio,
            new_ratio: ratio,
            effective_at: unixSeconds,
          })
        )
        .optional(),
    })
  ),
})
type ActivityRow = { group: string; at: number; change?: MarketRateChange }

function recentActivity(data: z.infer<typeof snapshot>): ActivityRow[] {
  const rows: ActivityRow[] = []
  for (const [group, entry] of Object.entries(data.groups)) {
    if (entry.is_new && entry.first_configured_at) {
      rows.push({ group, at: entry.first_configured_at })
    }
    for (const change of entry.rate_changes ?? []) {
      if (change.old_ratio !== change.new_ratio) {
        rows.push({ group, change, at: change.effective_at })
      }
    }
  }
  return rows.sort((a, b) => b.at - a.at)
}

export function GroupMarketActivityPreview() {
  const user = useAuthStore((state) => state.auth.user)
  const sid = useAuthStore((state) => state.auth.session?.sid)
  if (user?.role !== ROLE.SUPER_ADMIN) return null
  return (
    <ActivityPreview
      key={`${user.id}:${sid ?? ''}`}
      userId={user.id}
      sid={sid}
    />
  )
}

function ActivityPreview(props: { userId: number; sid?: string }) {
  const { t } = useTranslation()
  const titleId = useId()
  const [expanded, setExpanded] = useState(false)
  const activity = useQuery({
    queryKey: ['admin-group-market-activity', props.userId, props.sid],
    queryFn: async ({ signal }) => {
      const response = await api.get('/api/option/group-catalog/activity', {
        signal,
        disableDuplicate: true,
      })
      const result = snapshot.safeParse(
        requireServerSuccess(response.data)?.data
      )
      if (!result.success) {
        throw new Error('Could not load marketplace activity')
      }
      return recentActivity(result.data)
    },
    meta: { errorToast: false },
  })
  const rows = activity.data ?? []
  return (
    <Card role='region' aria-labelledby={titleId} className='mt-4'>
      <CardHeader>
        <CardTitle id={titleId}>{t('Marketplace activity')}</CardTitle>
        <CardDescription>
          {t(
            'Saved group additions and base rate changes appear automatically. Display edits, access rules and personal rates are not included.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='min-w-0 space-y-3'>
        {activity.isPending && (
          <LoadingState inline message={t('Loading...')} />
        )}
        {activity.isError && (
          <ErrorState
            className='min-h-28'
            title={t('Could not load marketplace activity')}
            description={getServerErrorMessage(activity.error)}
            onRetry={() => void activity.refetch()}
          />
        )}
        {activity.isSuccess &&
          (rows.length === 0 ? (
            <EmptyState
              className='min-h-24'
              title={t('No recent group activity')}
            />
          ) : (
            <>
              <StaticDataTable
                data={expanded ? rows : rows.slice(0, 8)}
                getRowKey={(row, index) => `${row.group}:${row.at}:${index}`}
                columns={[
                  {
                    id: 'group',
                    header: t('Billing group'),
                    cellClassName: 'whitespace-normal break-all font-medium',
                    cell: (row) => row.group,
                  },
                  {
                    id: 'event',
                    header: t('Change'),
                    cell: (row) =>
                      row.change ? (
                        <MarketRateChangeLine
                          change={row.change}
                          showTime={false}
                        />
                      ) : (
                        t('Group added')
                      ),
                  },
                  {
                    id: 'time',
                    header: t('Effective time'),
                    cell: (row) => (
                      <time dateTime={new Date(row.at * 1000).toISOString()}>
                        {formatTimestampToDate(row.at)}
                      </time>
                    ),
                  },
                ]}
              />
              {rows.length > 8 && (
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  aria-expanded={expanded}
                  onClick={() => setExpanded(!expanded)}
                >
                  {expanded ? t('Collapse') : t('View all')}
                </Button>
              )}
            </>
          ))}
        <p className='text-muted-foreground text-xs'>
          {t(
            'New groups: 7 days. Base rate changes: up to 10 per group in the last 90 days.'
          )}
        </p>
      </CardContent>
    </Card>
  )
}

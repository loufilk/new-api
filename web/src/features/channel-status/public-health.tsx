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
import { Link } from '@tanstack/react-router'
import { Activity } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { PublicLayout } from '@/components/layout'
import { Button, buttonVariants } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { PublicLineHealth } from './public-line-health'
import { usePublicLineHealth } from './use-public-line-health'

// Only the administrator's explicitly published monitor names and latest heartbeat.
// This is not the authenticated group-health API; it doesn't publish group grants,
// private routes, request counts or account-specific quotes.
const publicMonitors = z.object({
  success: z.literal(true),
  data: z.array(
    z.object({
      categoryName: z.string(),
      state: z.enum(['ready', 'stale', 'unavailable']),
      updated_at: z.number().optional(),
      monitors: z
        .array(z.object({ name: z.string(), status: z.number().int() }))
        .transform((monitors) => {
          // Kuma names are public but not guaranteed unique. Use each name's
          // occurrence number rather than the position in the entire list.
          const seen = new Map<string, number>()
          return monitors.map((monitor) => {
            const occurrence = (seen.get(monitor.name) ?? 0) + 1
            seen.set(monitor.name, occurrence)
            return {
              ...monitor,
              key: JSON.stringify([monitor.name, occurrence]),
            }
          })
        }),
    })
  ),
})

const heartbeatLabels: Readonly<Record<number, string>> = {
  0: 'Unavailable',
  1: 'Operational',
  2: 'Pending',
  3: 'Maintenance',
}

export function PublicChannelHealthPage() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [refresh, setRefresh] = useState(60_000)
  const lines = usePublicLineHealth(refresh)
  const query = useQuery({
    queryKey: ['channel-health', 'public-monitors'],
    queryFn: async ({ signal }) => {
      const response = await fetch('/api/uptime/status', {
        signal,
        credentials: 'omit',
        cache: 'no-store',
      })
      if (!response.ok) throw new Error('Public monitoring unavailable')
      return publicMonitors.parse(await response.json()).data
    },
    retry: false,
    gcTime: 0,
    refetchOnMount: 'always',
    refetchInterval: refresh || false,
    refetchIntervalInBackground: false,
    meta: { errorToast: false, redirectOnInternalServerError: false },
  })
  return (
    <PublicLayout>
      <section className='mx-auto w-full max-w-5xl space-y-6 px-4 py-12 sm:px-6'>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <h1 className='text-2xl font-semibold'>{t('Channel status')}</h1>
          <div className='flex flex-wrap items-center gap-2'>
            <label className='text-sm'>
              <span className='sr-only'>{t('Auto-refresh interval')}</span>
              <select
                className='bg-background h-9 rounded-md border px-2'
                value={refresh}
                onChange={(event) =>
                  setRefresh(Number(event.currentTarget.value))
                }
              >
                <option value={60_000}>
                  {t('Every {{seconds}} seconds', {
                    seconds: formatNumber(60, locale),
                  })}
                </option>
                <option value={120_000}>
                  {t('Every {{seconds}} seconds', {
                    seconds: formatNumber(120, locale),
                  })}
                </option>
                <option value={300_000}>
                  {t('Every {{seconds}} seconds', {
                    seconds: formatNumber(300, locale),
                  })}
                </option>
                <option value={0}>{t('Auto-refresh off')}</option>
              </select>
            </label>
            <Button
              variant='outline'
              disabled={query.isFetching || lines.isFetching}
              onClick={() => {
                void query.refetch()
                void lines.refetch()
              }}
            >
              {t('Refresh')}
            </Button>
          </div>
        </div>
        <PublicLineHealth query={lines} />
        <h2 className='text-xl font-semibold'>{t('Public monitors')}</h2>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Only publicly configured monitors are shown. Sign in for the status of your groups.'
          )}
        </p>
        {query.isError && (
          <p role='alert'>{t('Unable to load public monitoring data.')}</p>
        )}
        {query.isPending && <p role='status'>{t('Loading...')}</p>}
        {!query.isError && query.data?.length === 0 && (
          <p role='status'>{t('No public monitoring data')}</p>
        )}
        {!query.isError &&
          query.data?.map((group) => {
            const updateLabel = group.updated_at
              ? new Date(group.updated_at * 1000).toLocaleString(locale)
              : ''
            return (
              <Card className='gap-3 p-4' key={group.categoryName}>
                <h3 className='flex items-center gap-2 font-medium'>
                  <Activity size={16} aria-hidden />
                  {group.categoryName}
                </h3>
                {group.state === 'stale' && (
                  <p className='text-muted-foreground text-sm'>
                    {t('Showing the last reported status.')}
                    {updateLabel && ` ${updateLabel}`}
                  </p>
                )}
                {group.state === 'unavailable' && <p>{t('Unavailable')}</p>}
                {group.state !== 'unavailable' &&
                  group.monitors.length === 0 && (
                    <p>{t('No public monitoring data')}</p>
                  )}
                {group.state !== 'unavailable' && group.monitors.length > 0 && (
                  <ul className='divide-y'>
                    {group.monitors.map((monitor) => (
                      <li
                        key={monitor.key}
                        className='flex items-center justify-between gap-3 py-2'
                      >
                        <span className='min-w-0 break-words'>
                          {monitor.name}
                        </span>
                        <span className='shrink-0'>
                          {t(heartbeatLabels[monitor.status] ?? 'Unknown')}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </Card>
            )
          })}
        <Link
          to='/channel-status'
          className={buttonVariants({ variant: 'outline' })}
        >
          {t('View my group status')}
        </Link>
      </section>
    </PublicLayout>
  )
}

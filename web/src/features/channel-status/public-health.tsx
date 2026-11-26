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
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { PublicLayout } from '@/components/layout'
import { Button, buttonVariants } from '@/components/ui/button'
import { Card } from '@/components/ui/card'

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
      monitors: z.array(
        z.object({ name: z.string(), status: z.number().int() })
      ),
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
  const { t } = useTranslation()
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
    refetchInterval: 60_000,
    refetchIntervalInBackground: false,
    meta: { errorToast: false, redirectOnInternalServerError: false },
  })
  return (
    <PublicLayout>
      <section className='mx-auto w-full max-w-4xl space-y-5 px-2 py-12 sm:px-6'>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <h1 className='text-2xl font-semibold'>{t('Channel status')}</h1>
          <Button
            variant='outline'
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
          >
            {t('Refresh')}
          </Button>
        </div>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Only publicly configured monitors are shown. Sign in for the status of your groups.'
          )}
        </p>
        {query.isError ? (
          <p role='alert'>{t('Unable to load public monitoring data.')}</p>
        ) : query.isPending ? (
          <p role='status'>{t('Loading...')}</p>
        ) : query.data.length === 0 ? (
          <p role='status'>{t('No public monitoring data')}</p>
        ) : (
          query.data.map((group, i) => (
            <Card className='gap-3 p-4' key={i}>
              <h2 className='flex items-center gap-2 font-medium'>
                <Activity size={16} aria-hidden />
                {group.categoryName}
              </h2>
              {group.state === 'stale' && (
                <p className='text-muted-foreground text-sm'>
                  {t('Showing the last reported status.')}
                  {group.updated_at
                    ? ' ' + new Date(group.updated_at * 1000).toLocaleString()
                    : ''}
                </p>
              )}
              {group.state === 'unavailable' ? (
                <p>{t('Unavailable')}</p>
              ) : group.monitors.length === 0 ? (
                <p>{t('No public monitoring data')}</p>
              ) : (
                <ul className='divide-y'>
                  {group.monitors.map((monitor, j) => (
                    <li
                      key={j}
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
          ))
        )}
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

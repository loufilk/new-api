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
  Activity,
  CheckCircle2,
  CircleHelp,
  RefreshCw,
  Search,
  TriangleAlert,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Main } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { getTokenAutoGroups } from '@/features/keys/api'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'

import { GroupHealthCard } from './group-card'
import { GroupDetailDialog } from './group-detail-dialog'
import { filterHealthGroups, groupHealth } from './health'

export function ChannelStatusPage() {
  return (
    <Main>
      <div className='min-h-0 flex-1 overflow-auto p-4 sm:p-6'>
        <ChannelStatusDashboard />
      </div>
    </Main>
  )
}

export function ChannelStatusDashboard() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [search, setSearch] = useState('')
  const [state, setState] = useState('all')
  const [selected, setSelected] = useState<string | null>(null)
  const query = useQuery({
    queryKey: ['token-auto-groups'],
    queryFn: async () => requireServerSuccess(await getTokenAutoGroups()).data,
    refetchInterval: 60_000,
    staleTime: 30_000,
  })
  const groups = query.data?.group_details ?? []
  const filtered = filterHealthGroups(groups, search, state)
  const selectedGroup = groups.find((group) => group.name === selected)
  const summaries = [
    {
      title: 'Accessible groups',
      value: groups.length,
      icon: Activity,
      color: 'text-primary',
    },
    {
      title: 'Healthy',
      value: groups.filter((group) => groupHealth(group) === 'healthy').length,
      icon: CheckCircle2,
      color: 'text-emerald-600 dark:text-emerald-400',
    },
    {
      title: 'Needs attention',
      value: groups.filter((group) => groupHealth(group) === 'degraded').length,
      icon: TriangleAlert,
      color: 'text-amber-600 dark:text-amber-400',
    },
    {
      title: 'No samples',
      value: groups.filter((group) => groupHealth(group) === 'unknown').length,
      icon: CircleHelp,
      color: 'text-muted-foreground',
    },
  ]
  return (
    <div className='mx-auto w-full max-w-[1440px] space-y-6'>
      <header className='flex flex-wrap items-start justify-between gap-4'>
        <div className='space-y-1.5'>
          <h1 className='text-2xl font-semibold tracking-tight'>
            {t('Channel status')}
          </h1>
          <p className='text-muted-foreground max-w-2xl text-sm'>
            {t(
              'Compare your available groups, effective prices and real request outcomes in one place.'
            )}
          </p>
        </div>
        <Button
          variant='outline'
          onClick={() => void query.refetch()}
          disabled={query.isFetching}
        >
          <RefreshCw className='size-4' aria-hidden />
          {t('Refresh')}
        </Button>
      </header>
      <div className='grid grid-cols-2 gap-3 xl:grid-cols-4'>
        {summaries.map((summary) => (
          <div
            key={summary.title}
            className='bg-card space-y-3 rounded-2xl border p-4'
          >
            <div className='text-muted-foreground flex items-center justify-between gap-2 text-xs'>
              <span>{t(summary.title)}</span>
              <summary.icon className={`size-4 ${summary.color}`} aria-hidden />
            </div>
            <p className='text-2xl font-semibold tabular-nums'>
              {!query.data ? '—' : formatNumber(summary.value, locale)}
            </p>
          </div>
        ))}
      </div>
      {query.data?.metrics_available === false && (
        <Alert>
          <AlertDescription>
            {t(
              'Performance collection is unavailable. Group availability and your effective prices remain visible.'
            )}
          </AlertDescription>
        </Alert>
      )}
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <Tabs
          value={state}
          onValueChange={(value) => setState(String(value))}
          className='max-w-full overflow-x-auto'
        >
          <TabsList>
            <TabsTrigger value='all'>{t('All groups')}</TabsTrigger>
            <TabsTrigger value='healthy'>{t('Healthy')}</TabsTrigger>
            <TabsTrigger value='degraded'>{t('Needs attention')}</TabsTrigger>
            <TabsTrigger value='unknown'>{t('No samples')}</TabsTrigger>
            <TabsTrigger value='unavailable'>{t('Unavailable')}</TabsTrigger>
          </TabsList>
        </Tabs>
        <div className='relative w-full sm:w-72'>
          <Search
            className='text-muted-foreground pointer-events-none absolute top-2.5 left-3 size-4'
            aria-hidden
          />
          <Input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            className='pl-9'
            placeholder={t('Search group name or description')}
            aria-label={t('Search groups')}
          />
        </div>
      </div>
      {query.isPending && <LoadingState />}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {!query.isPending && !query.isError && filtered.length === 0 && (
        <EmptyState
          icon={Activity}
          title={t('No matching groups')}
          description={t('Try another search or status filter.')}
          bordered
        />
      )}
      <div className='grid grid-cols-1 gap-4 md:grid-cols-2 2xl:grid-cols-3'>
        {filtered.map((group) => (
          <GroupHealthCard
            key={group.name}
            group={group}
            onDetails={() => setSelected(group.name)}
          />
        ))}
      </div>
      <p className='text-muted-foreground text-xs leading-relaxed'>
        {t(
          'Automatically refreshed every 60 seconds. Healthy means at least 95% success in the displayed window. Gray bars indicate no samples, not an outage.'
        )}
      </p>
      {selectedGroup && (
        <GroupDetailDialog
          group={selectedGroup}
          windowStart={query.data?.window_start}
          windowEnd={query.data?.window_end}
          onClose={() => setSelected(null)}
        />
      )}
    </div>
  )
}

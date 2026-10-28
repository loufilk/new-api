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
import { Activity, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { toIntlLocale } from '@/i18n/languages'

import { getMonitorSnapshot } from './api'
import { ResultDialog } from './result-dialog'
import { resultTime } from './result-state'
import { TargetCard } from './target-card'
import type { MonitorResult } from './types'

export function MonitorDashboard() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<MonitorResult | null>(null)
  const query = useQuery({
    queryKey: ['model-monitor'],
    queryFn: ({ signal }) => getMonitorSnapshot(signal),
    staleTime: 30_000,
    refetchInterval: 60_000,
    retry: false,
    meta: { errorToast: false },
  })
  const snapshot = query.data
  // Failed refetches never leave previously fetched data looking live.
  const stale = snapshot?.stale || query.isError
  const expired =
    !!snapshot?.enabled &&
    Date.now() / 1000 - Math.min(snapshot.fetched_at, snapshot.observed_at) >
      600
  const targets =
    snapshot?.targets.filter((target) =>
      `${target.model} ${target.name ?? ''}`
        .toLowerCase()
        .includes(search.trim().toLowerCase())
    ) ?? []
  const results = [...(snapshot?.results ?? [])].sort(
    (a, b) =>
      b.started_at - a.started_at || a.benchmark.localeCompare(b.benchmark)
  )
  const currentSelection = snapshot?.results.find(
    (result) => result.id === selected?.id
  )

  return (
    <div className='mx-auto max-w-7xl space-y-6 py-4'>
      <header className='flex flex-wrap items-start justify-between gap-4'>
        <div className='min-w-0 space-y-2'>
          <Badge variant='secondary'>
            <Activity className='size-3' aria-hidden />
            {t('monitor.reference')}
          </Badge>
          <h1 className='text-3xl font-semibold tracking-tight'>
            {snapshot?.title || t('monitor.title')}
          </h1>
          <p className='text-muted-foreground text-sm'>
            {snapshot?.description || t('monitor.subtitle')}
          </p>
        </div>
        <Button
          variant='outline'
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          <RefreshCw
            className={query.isFetching ? 'animate-spin' : ''}
            aria-hidden
          />
          {t('monitor.refresh')}
        </Button>
      </header>
      <Alert role='note'>
        <AlertDescription>{t('monitor.scope')}</AlertDescription>
      </Alert>
      {query.isPending && <LoadingState message={t('monitor.loading')} />}
      {((!snapshot && query.isError) || expired) && (
        <ErrorState
          title={t('monitor.unavailable')}
          description={t('monitor.tryLater')}
          onRetry={() => void query.refetch()}
        />
      )}
      {snapshot && !expired && (
        <>
          {stale && (
            <Alert>
              <AlertTitle>{t('monitor.stale')}</AlertTitle>
              <AlertDescription>
                {t('monitor.staleDescription')}
              </AlertDescription>
            </Alert>
          )}
          <div
            className='text-muted-foreground flex flex-wrap gap-3 text-sm'
            role='status'
          >
            <span>
              {t('monitor.updatedAt', {
                time: resultTime(snapshot.observed_at, locale),
              })}
            </span>
            {snapshot.running && <span>{t('monitor.running')}</span>}
          </div>
          {!snapshot.enabled ? (
            <EmptyState title={t('monitor.disabled')} />
          ) : (
            <>
              <div className='max-w-sm space-y-2'>
                <Label htmlFor='monitor-search'>{t('monitor.search')}</Label>
                <Input
                  id='monitor-search'
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  placeholder={t('monitor.searchPlaceholder')}
                />
              </div>
              {targets.length === 0 ? (
                <EmptyState title={t('monitor.noMatches')} />
              ) : (
                <div className='grid grid-cols-1 items-start gap-5 md:grid-cols-2 xl:grid-cols-3'>
                  {targets.map((target) => (
                    <TargetCard
                      key={target.id}
                      target={target}
                      results={results.filter(
                        (result) => result.target_id === target.id
                      )}
                      onSelect={setSelected}
                    />
                  ))}
                </div>
              )}
            </>
          )}
        </>
      )}
      {currentSelection && !expired && (
        <ResultDialog
          result={currentSelection}
          onClose={() => setSelected(null)}
        />
      )}
    </div>
  )
}

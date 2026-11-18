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
import { Activity, ArrowUpRight, RefreshCw, Search } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { PublicLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { getPerfMetricsSummary } from '@/features/performance-metrics/api'
import {
  formatLatency,
  formatThroughput,
  formatUptimePct,
  getSuccessRateTextClass,
} from '@/features/performance-metrics/lib/format'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'
import { formatDateTimeObject } from '@/lib/time'

export function PublicHealthPage() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [search, setSearch] = useState('')
  const query = useQuery({
    queryKey: ['health', 'public', 24],
    queryFn: async () =>
      requireServerSuccess(await getPerfMetricsSummary(24)).data,
    staleTime: 30_000,
    refetchInterval: 60_000,
  })
  const summary = query.data?.summary
  const models = (query.data?.models ?? []).filter((model) =>
    model.model_name
      .toLocaleLowerCase()
      .includes(search.trim().toLocaleLowerCase())
  )
  return (
    <PublicLayout>
      <div className='mx-auto max-w-[1280px] space-y-6 pb-12'>
        <header className='flex flex-wrap items-start justify-between gap-4 py-5'>
          <div className='space-y-2'>
            <div className='text-primary flex items-center gap-2 text-sm font-medium'>
              <Activity className='size-4' aria-hidden />
              {t('Platform health')}
            </div>
            <h1 className='text-3xl font-semibold tracking-tight'>
              {t('Real traffic. Visible performance.')}
            </h1>
            <p className='text-muted-foreground max-w-2xl text-sm'>
              {t(
                'Model success rates, response times and throughput from the last 24 hours.'
              )}
            </p>
          </div>
          <div className='flex flex-wrap gap-2'>
            <Button
              variant='outline'
              onClick={() => void query.refetch()}
              disabled={query.isFetching}
            >
              <RefreshCw className='size-4' aria-hidden />
              {t('Refresh')}
            </Button>
            <Button render={<Link to='/channel-status' />}>
              {t('My group status')}
              <ArrowUpRight className='size-4' aria-hidden />
            </Button>
          </div>
        </header>
        <div className='grid gap-3 sm:grid-cols-3'>
          <div className='bg-card rounded-2xl border p-5'>
            <p className='text-muted-foreground text-sm'>{t('Success rate')}</p>
            <p
              className={`mt-3 text-3xl font-semibold tabular-nums ${getSuccessRateTextClass(summary?.success_rate ?? Number.NaN)}`}
            >
              {formatUptimePct(summary?.success_rate ?? Number.NaN)}
            </p>
          </div>
          <div className='bg-card rounded-2xl border p-5'>
            <p className='text-muted-foreground text-sm'>
              {t('Average latency')}
            </p>
            <p className='mt-3 text-3xl font-semibold tabular-nums'>
              {formatLatency(summary?.avg_latency_ms ?? 0)}
            </p>
          </div>
          <div className='bg-card rounded-2xl border p-5'>
            <p className='text-muted-foreground text-sm'>{t('Throughput')}</p>
            <p className='mt-3 text-3xl font-semibold tabular-nums'>
              {formatThroughput(summary?.avg_tps ?? 0)}
            </p>
          </div>
        </div>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <h2 className='text-lg font-semibold'>{t('Model performance')}</h2>
          <div className='relative w-full sm:w-80'>
            <Search
              aria-hidden
              className='text-muted-foreground absolute top-2.5 left-3 size-4'
            />
            <Input
              aria-label={t('Search models')}
              placeholder={t('Search models')}
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              className='pl-9'
            />
          </div>
        </div>
        {query.isPending && <LoadingState />}
        {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
        {!query.isPending && !query.isError && models.length === 0 && (
          <EmptyState
            icon={Activity}
            title={t('No performance samples to display')}
            description={t(
              'Only models with recorded traffic appear in this window.'
            )}
            bordered
          />
        )}
        <div className='grid gap-4 md:grid-cols-2 xl:grid-cols-3'>
          {models.map((model) => (
            <article
              key={model.model_name}
              className='bg-card min-w-0 rounded-2xl border p-5 shadow-xs'
            >
              <div className='flex items-start justify-between gap-3'>
                <h3 className='min-w-0 font-semibold [overflow-wrap:anywhere]'>
                  {model.model_name}
                </h3>
                <span
                  className={`shrink-0 text-sm font-semibold tabular-nums ${getSuccessRateTextClass(model.success_rate)}`}
                >
                  {formatUptimePct(model.success_rate)}
                </span>
              </div>
              <dl className='mt-5 grid grid-cols-2 gap-3 text-sm'>
                <div>
                  <dt className='text-muted-foreground text-xs'>
                    {t('Average latency')}
                  </dt>
                  <dd className='mt-1 font-medium tabular-nums'>
                    {formatLatency(model.avg_latency_ms)}
                  </dd>
                </div>
                <div>
                  <dt className='text-muted-foreground text-xs'>
                    {t('Throughput')}
                  </dt>
                  <dd className='mt-1 font-medium tabular-nums'>
                    {formatThroughput(model.avg_tps)}
                  </dd>
                </div>
              </dl>
              <div className='mt-5 flex items-center justify-between gap-2 border-t pt-3'>
                <p className='text-muted-foreground text-xs'>
                  {model.request_count == null
                    ? t('Last 24 hours')
                    : t('{{samples}} recorded attempts', {
                        samples: formatNumber(model.request_count, locale),
                      })}
                </p>
                <Button
                  variant='ghost'
                  size='sm'
                  render={
                    <Link
                      to='/pricing/$modelId'
                      params={{ modelId: model.model_name }}
                    />
                  }
                >
                  {t('Model details')}
                  <ArrowUpRight className='size-3.5' aria-hidden />
                </Button>
              </div>
            </article>
          ))}
        </div>
        <footer className='text-muted-foreground space-y-1 text-xs leading-relaxed'>
          <p>
            {t(
              'Success rate excludes business rejections and includes the current partial hour.'
            )}
          </p>
          <p>
            {t(
              'Automatically refreshed every 60 seconds. Missing samples are not interpreted as availability.'
            )}
          </p>
          {query.data?.window_start && query.data?.window_end ? (
            <p>
              {formatDateTimeObject(new Date(query.data.window_start * 1000))} —{' '}
              {formatDateTimeObject(new Date(query.data.window_end * 1000))}
            </p>
          ) : null}
        </footer>
      </div>
    </PublicLayout>
  )
}

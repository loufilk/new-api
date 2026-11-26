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
import { type ReactNode, useId } from 'react'
import { useTranslation } from 'react-i18next'

import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { getSuccessRateDotClass } from '@/features/performance-metrics/lib/format'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'

import {
  useKeyGroupHealth,
  type KeyGroupHealth,
} from '../hooks/use-key-group-health'

function HealthMeasurements({ data }: { data: KeyGroupHealth }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const time = new Intl.DateTimeFormat(locale, {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    timeZoneName: 'short',
  })
  const percent = new Intl.NumberFormat(locale, {
    style: 'percent',
    maximumFractionDigits: 2,
  })
  const latency = new Intl.NumberFormat(locale, {
    style: 'unit',
    unit: 'millisecond',
    maximumFractionDigits: 2,
  })

  const latest = data.latest_bucket_start
  const success = data.request_count > 0 ? data.success_rate : null
  const ttft = data.ttft_sample_count > 0 ? data.average_ttft_ms : null
  const showTtftTrend = data.buckets.some(
    (bucket) => bucket.ttft_sample_count > 0 && bucket.average_ttft_ms != null
  )
  const maxTtft = Math.max(
    1,
    ...data.buckets.map((bucket) =>
      bucket.ttft_sample_count > 0 ? (bucket.average_ttft_ms ?? 0) : 0
    )
  )
  return (
    <div className='space-y-2.5'>
      <p className='flex flex-wrap items-baseline justify-between gap-x-2 text-xs'>
        <span className='text-muted-foreground'>{t('Enabled routes')}</span>
        <span className='font-medium'>
          {data.available ? t('Yes') : t('No')}
        </span>
      </p>
      {!data.metrics_available && (
        <p className='text-muted-foreground text-xs'>
          {t('Group health metrics are unavailable.')}
        </p>
      )}
      {data.metrics_available && data.request_count === 0 && (
        <p className='text-muted-foreground text-xs'>
          {t('No requests were observed in this window.')}
        </p>
      )}
      {data.metrics_available && data.request_count > 0 && (
        <>
          <dl className='grid grid-cols-2 gap-2 rounded-md border p-2.5'>
            <div className='min-w-0'>
              <dt className='text-muted-foreground text-xs'>
                {t('Success rate')}
              </dt>
              <dd className='text-sm font-semibold tabular-nums'>
                {success == null ? t('No data') : percent.format(success / 100)}
              </dd>
              <dd className='text-muted-foreground text-xs'>
                {t('Requests')}: {formatNumber(data.request_count, locale)}
              </dd>
            </div>
            <div className='min-w-0'>
              <dt className='text-muted-foreground text-xs'>
                {t('Average TTFT')}
              </dt>
              <dd className='text-sm font-semibold tabular-nums'>
                {ttft == null ? t('No data') : latency.format(ttft)}
              </dd>
              <dd className='text-muted-foreground text-xs'>
                {t('Samples')}: {formatNumber(data.ttft_sample_count, locale)}
              </dd>
            </div>
          </dl>
          <div className='space-y-1'>
            <p className='text-muted-foreground text-xs'>
              {t('Hourly success rate')}
            </p>
            {showTtftTrend && (
              <p className='text-muted-foreground text-xs'>
                {t('Hourly average TTFT')}
              </p>
            )}
            <ol
              className='flex min-w-0 gap-px'
              aria-label={t('Performance metrics for the last 24 hours')}
            >
              {data.buckets.map((bucket) => {
                const sampledSuccess =
                  bucket.request_count > 0 ? bucket.success_rate : null
                const sampledTtft =
                  bucket.ttft_sample_count > 0 ? bucket.average_ttft_ms : null

                const successLabel =
                  sampledSuccess == null
                    ? t('No data')
                    : percent.format(sampledSuccess / 100)
                const ttftLabel =
                  sampledTtft == null
                    ? t('No data')
                    : latency.format(sampledTtft)
                const label = [
                  time.format(bucket.bucket_start * 1000),
                  `${t('Success rate')}: ${successLabel}`,
                  `${t('Average TTFT')}: ${ttftLabel}`,
                ].join(' · ')
                return (
                  <li
                    key={bucket.bucket_start}
                    className='min-w-0 flex-1 space-y-1'
                    title={label}
                  >
                    <span className='sr-only'>{label}</span>
                    <span
                      aria-hidden='true'
                      className={cn(
                        'block h-2.5 rounded-xs',
                        sampledSuccess == null
                          ? 'bg-muted-foreground/25'
                          : getSuccessRateDotClass(sampledSuccess)
                      )}
                    />
                    {showTtftTrend && (
                      <span className='flex h-4 items-end' aria-hidden='true'>
                        <span
                          className={cn(
                            'block w-full rounded-xs',
                            sampledTtft == null
                              ? 'bg-muted-foreground/25 h-px'
                              : 'bg-sky-500 dark:bg-sky-400'
                          )}
                          style={
                            sampledTtft == null
                              ? undefined
                              : {
                                  height: `${Math.max(8, (sampledTtft / maxTtft) * 100)}%`,
                                }
                          }
                        />
                      </span>
                    )}
                  </li>
                )
              })}
            </ol>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Recent success-rate samples; gray bars indicate missing data.'
              )}
            </p>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Success rate excludes business rejections and includes the current partial hour.'
              )}
            </p>
            <p className='text-muted-foreground text-xs'>
              {t(
                'TTFT measures the first upstream event, which may precede visible output.'
              )}
            </p>
          </div>
        </>
      )}
      <div className='text-muted-foreground space-y-0.5 text-xs'>
        <p>
          {t('Last checked: {{time}}', {
            time: time.format(data.observed_at * 1000),
          })}
        </p>
        {data.metrics_available && latest != null && (
          <p>
            {t('Latest sampled hour: {{time}}', {
              time: time.format(latest * 1000),
            })}
          </p>
        )}
      </div>
    </div>
  )
}

export function KeyGroupHealthPreview({ groupId }: { groupId: string }) {
  const { t } = useTranslation()
  const headingId = useId()
  const { enabled, result } = useKeyGroupHealth(groupId)
  if (!enabled) return null

  let content: ReactNode
  if (result.isPaused || result.isError) {
    content = (
      <div className='space-y-1.5'>
        <p role='alert' className='text-muted-foreground text-xs'>
          {t('Could not load group health.')}
        </p>
        <Button
          type='button'
          variant='outline'
          size='xs'
          onClick={() => void result.refetch()}
        >
          {t('Retry')}
        </Button>
      </div>
    )
  } else if (result.isPending || result.isFetching) {
    content = <LoadingState inline size='sm' message={t('Loading...')} />
  } else {
    content = <HealthMeasurements data={result.data} />
  }

  return (
    <section
      aria-labelledby={headingId}
      className='min-w-0 space-y-2 border-t pt-3'
    >
      <h4 id={headingId} className='text-sm font-medium'>
        {t('24-hour group health')}
      </h4>
      {content}
    </section>
  )
}

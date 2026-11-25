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
import { memo } from 'react'
import { useTranslation } from 'react-i18next'

import { getSuccessRateDotClass } from '@/features/performance-metrics/lib/format'
import type {
  PerformanceSummary,
  SuccessRatePoint,
} from '@/features/performance-metrics/types'
import { toIntlLocale } from '@/i18n/languages'
import { cn } from '@/lib/utils'

import { marketMeasurements } from '../lib/market-measurements'

export type ModelPerfBadgeData = PerformanceSummary & {
  window_start?: number
  window_end?: number
  recent_success_series?: SuccessRatePoint[]
}
export interface ModelPerfBadgeProps extends React.HTMLAttributes<HTMLDivElement> {
  perf: ModelPerfBadgeData | undefined
}

export const ModelPerfBadge = memo(function ModelPerfBadge(
  props: ModelPerfBadgeProps
) {
  const { t, i18n } = useTranslation()
  const values = marketMeasurements(props.perf)
  const start = props.perf?.window_start
  const end = props.perf?.window_end
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const windowTitle =
    start && end
      ? [start, end]
          .map((ts) => new Date(ts * 1000).toLocaleString(locale))
          .join(' – ')
      : undefined
  // Per-model series belongs to the same window as the weighted summary.
  // The API's optional top-level seven-day history is intentionally not used.
  const samples = (props.perf?.recent_success_series ?? [])
    .filter(
      (point) =>
        Number.isFinite(point.ts) &&
        Number.isFinite(point.success_rate) &&
        point.success_rate >= 0 &&
        point.success_rate <= 100 &&
        (start == null || point.ts >= start) &&
        (end == null || point.ts <= end)
    )
    .sort((a, b) => a.ts - b.ts)
    .slice(-24)
  return (
    <div
      aria-label={t('Performance')}
      className={cn(
        'flex w-full min-w-0 flex-wrap items-end gap-2',
        props.className
      )}
    >
      <div className='min-w-0 flex-1 basis-full'>
        <div className='text-muted-foreground mb-2 flex flex-wrap items-center justify-between gap-1 text-xs'>
          <span title={windowTitle}>
            {t('24 hourly buckets, including the current hour')}
          </span>
          {samples.length > 0 && (
            <span
              role='img'
              aria-label={t(
                'Request success rate sampled over the last 24 hours'
              )}
              className='flex h-2.5 items-end gap-px'
            >
              {samples.map((point) => (
                <span
                  key={point.ts}
                  aria-hidden
                  className={cn(
                    'h-full w-[3px] rounded-xs',
                    getSuccessRateDotClass(point.success_rate)
                  )}
                />
              ))}
            </span>
          )}
        </div>
        <dl className='grid grid-cols-2 gap-2 @sm/market-card:grid-cols-4'>
          {[
            [t('Average latency'), values.latency],
            [t('Average TTFT'), values.ttft],
            ['TPS', values.tps],
            [t('Success rate'), values.success],
          ].map(([label, value]) => (
            <div className='min-w-0' key={label}>
              <dt className='text-muted-foreground text-xs'>{label}</dt>
              <dd
                className={cn(
                  'text-sm font-medium tabular-nums',
                  !value && 'text-muted-foreground'
                )}
              >
                {value ?? t('Unavailable')}
              </dd>
            </div>
          ))}
        </dl>
      </div>
      {props.children}
    </div>
  )
})

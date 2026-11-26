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
import { HeartPulse, Timer } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  staticDataTableClassNames as tableStyles,
} from '@/components/data-table'
import type { PerformanceSeriesPoint } from '@/features/performance-metrics/types'
import { cn } from '@/lib/utils'

import { useMarketModelMetrics } from '../hooks/use-market-metrics'
import { marketMeasurements } from '../lib/market-measurements'
import type { PricingModel } from '../types'
import { CatalogGroupBadge } from './catalog-group-badge'
import { LatencyTrendChart, UptimeTrendChart } from './model-details-charts'
import { UptimeSparkline } from './model-details-uptime-sparkline'
import { ModelPerfBadge } from './model-perf-badge'

function toSuccessSeries(series: PerformanceSeriesPoint[]) {
  return series
    .filter(
      (point) =>
        Number.isFinite(point.ts) && marketMeasurements(point).success !== null
    )
    .map((point) => ({
      date: new Date(point.ts * 1000).toISOString(),
      uptime_pct: point.success_rate,
    }))
}

export function ModelDetailsPerformance(props: { model: PricingModel }) {
  const { t } = useTranslation()
  const metricsQuery = useMarketModelMetrics(props.model.model_name)
  const groups = useMemo(
    () =>
      (metricsQuery.data?.data.groups ?? []).filter((group) =>
        props.model.enable_groups?.includes(group.group)
      ),
    [metricsQuery.data, props.model.enable_groups]
  )
  const summary = metricsQuery.data?.data.summary
  const series = useMemo(
    () => metricsQuery.data?.data.series ?? [],
    [metricsQuery.data]
  )
  const performances = useMemo(
    () =>
      groups.map((group) => ({
        group: group.group,
        ...marketMeasurements(group),
        successRate: group.success_rate,
        series: toSuccessSeries(group.series),
      })),
    [groups]
  )
  const latencySeries = useMemo(
    () =>
      series
        .filter(
          (p) => Number.isFinite(p.ts) && marketMeasurements(p).ttft !== null
        )
        .map((p) => ({
          timestamp: new Date(p.ts * 1000).toISOString(),
          group: 'TTFT',
          ttft_ms: p.avg_ttft_ms,
        })),
    [series]
  )
  const uptimeSeries = useMemo(() => toSuccessSeries(series), [series])

  if (
    metricsQuery.isLoading ||
    (!summary && performances.length === 0 && series.length === 0)
  ) {
    return (
      <div className='text-muted-foreground rounded-lg border p-6 text-center text-sm'>
        {t('Performance data is not yet available for this model.')}
      </div>
    )
  }

  const perf = summary
    ? {
        ...summary,
        window_start: metricsQuery.data?.data.window_start,
        window_end: metricsQuery.data?.data.window_end,
      }
    : undefined
  return (
    <div className='flex flex-col gap-4'>
      <ModelPerfBadge
        perf={perf}
        className='@container/market-card rounded-lg border p-3'
      />

      {performances.length > 0 && (
        <section>
          <SectionHeader
            icon={HeartPulse}
            title={t('Per-group performance')}
            description={t('Average latency, TTFT, TPS, and success rate')}
          />
          <StaticDataTable
            className='rounded-lg'
            tableClassName='text-sm'
            headerRowClassName={tableStyles.compactHeaderRow}
            data={performances}
            getRowKey={(perf) => perf.group}
            columns={[
              {
                id: 'group',
                header: t('Group'),
                className: tableStyles.compactHeaderCell,
                cellClassName: tableStyles.compactCell,
                cell: (perf) => (
                  <CatalogGroupBadge
                    group={perf.group}
                    catalog={props.model.group_catalog}
                  />
                ),
              },
              {
                id: 'tps',
                header: 'TPS',
                className: tableStyles.compactHeaderCellRight,
                cellClassName: tableStyles.compactNumericCell,
                cell: (perf) => perf.tps ?? t('Unavailable'),
              },
              {
                id: 'ttft',
                header: t('Average TTFT'),
                className: tableStyles.compactHeaderCellRight,
                cellClassName: tableStyles.compactNumericCell,
                cell: (perf) => perf.ttft ?? t('Unavailable'),
              },
              {
                id: 'latency',
                header: t('Average latency'),
                className: tableStyles.compactHeaderCellRight,
                cellClassName: tableStyles.compactMutedNumericCell,
                cell: (perf) => perf.latency ?? t('Unavailable'),
              },
              {
                id: 'success',
                header: t('Success rate'),
                className: cn(tableStyles.compactHeaderCell, 'min-w-[180px]'),
                cellClassName: tableStyles.compactCell,
                cell: (perf) => (
                  <UptimeSparkline
                    size='sm'
                    series={perf.series}
                    emptyLabel={perf.success ?? t('Unavailable')}
                    overallSuccessRate={perf.successRate}
                  />
                ),
              },
            ]}
          />
        </section>
      )}

      {latencySeries.length > 0 && (
        <section>
          <SectionHeader
            icon={Timer}
            title={t('Average TTFT')}
            description={t('24 hourly buckets, including the current hour')}
          />
          <LatencyTrendChart series={latencySeries} />
        </section>
      )}

      {uptimeSeries.length > 0 && (
        <section>
          <SectionHeader
            icon={HeartPulse}
            title={t('Success rate')}
            description={t(
              'Success rate excludes business rejections and includes the current partial hour.'
            )}
          />
          <UptimeTrendChart series={uptimeSeries} />
        </section>
      )}
    </div>
  )
}

function SectionHeader(props: {
  icon: React.ComponentType<{ className?: string }>
  title: string
  description?: string
  accent?: React.ReactNode
}) {
  const Icon = props.icon
  return (
    <div className='mb-2 flex flex-wrap items-center justify-between gap-2'>
      <div className='flex min-w-0 items-center gap-2'>
        <Icon className='text-muted-foreground/70 size-3.5 shrink-0' />
        <div className='min-w-0'>
          <div className='text-foreground text-sm font-semibold'>
            {props.title}
          </div>
          {props.description && (
            <p className='text-muted-foreground/80 text-xs'>
              {props.description}
            </p>
          )}
        </div>
      </div>
      {props.accent && (
        <div className='shrink-0 text-xs font-medium'>{props.accent}</div>
      )}
    </div>
  )
}

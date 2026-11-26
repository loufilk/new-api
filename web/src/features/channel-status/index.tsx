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
import { Link } from '@tanstack/react-router'
import { Activity, KeyRound, RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout/components/section-page-layout'
import { LoadingState } from '@/components/loading-state'
import { Button, buttonVariants } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  getSuccessRateDotClass,
  getSuccessRateLevel,
} from '@/features/performance-metrics/lib/format'
import { MarketGroupIcon } from '@/features/pricing/components/market-group-icon'
import {
  MARKET_PLATFORMS,
  type MarketPlatform,
} from '@/features/pricing/lib/market-groups'
import { toIntlLocale } from '@/i18n/languages'
import { cn } from '@/lib/utils'

import {
  CHANNEL_STATUS_REFRESH_MS,
  useChannelStatus,
  type ChannelGroupHealth,
  type ChannelStatusGroup,
} from './use-channel-status'

const sorts = [
  ['ratio', 'Lowest multiplier'],
  ['success', 'Highest success rate'],
  ['ttft', 'Fastest first token'],
  ['cache', 'Highest cache hit rate'],
  ['status', 'Operational first'],
] as const
type Sort = (typeof sorts)[number][0]

const ranges = [
  ['24h', 'Last 24 hours'],
  ['30d', 'Last 30 days'],
  ['12w', 'Last 12 weeks'],
] as const
type Range = (typeof ranges)[number][0]

function healthStatus(health: ChannelGroupHealth) {
  if (!health.available) return 'offline'
  if (!health.metrics_available || health.success_rate == null) return 'unknown'
  const level = getSuccessRateLevel(health.success_rate)
  return level === 'excellent' || level === 'good' ? 'healthy' : 'degraded'
}

const statuses = {
  healthy: ['Operational', 'text-emerald-700 dark:text-emerald-400'],
  degraded: ['Degraded', 'text-amber-700 dark:text-amber-400'],
  offline: ['No enabled route', 'text-destructive'],
  unknown: ['Insufficient observations', 'text-muted-foreground'],
} as const

function sortGroups(groups: ChannelStatusGroup[], sort: Sort, locale?: string) {
  const missing = Number.POSITIVE_INFINITY
  const statusOrder = { healthy: 0, degraded: 1, unknown: 2, offline: 3 }
  function rank({ ratio, health }: ChannelStatusGroup) {
    switch (sort) {
      case 'ratio':
        return ratio
      case 'success':
        return health.metrics_available && health.success_rate != null
          ? -health.success_rate
          : missing
      case 'ttft':
        return health.metrics_available
          ? (health.average_ttft_ms ?? missing)
          : missing
      case 'status':
        return statusOrder[healthStatus(health)]
      // The existing group instrumentation does not provide cache hit rates.
      case 'cache':
        return missing
    }
  }
  return [...groups].sort((a, b) => {
    const aRank = rank(a)
    const bRank = rank(b)
    return (
      (aRank === bRank ? 0 : aRank - bRank) ||
      a.label.localeCompare(b.label, locale) ||
      a.name.localeCompare(b.name, locale)
    )
  })
}

function useFormats() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const percent = new Intl.NumberFormat(locale, {
    style: 'percent',
    maximumFractionDigits: 2,
  })
  const milliseconds = new Intl.NumberFormat(locale, {
    style: 'unit',
    unit: 'millisecond',
    maximumFractionDigits: 2,
  })
  const timestamp = new Intl.DateTimeFormat(locale, {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    timeZoneName: 'short',
  })
  const multiplier = new Intl.NumberFormat(locale, { maximumFractionDigits: 4 })
  return {
    locale,
    rate: (value: number | null) =>
      value == null ? t('No data') : percent.format(value / 100),
    latency: (value: number | null) =>
      value == null ? t('No data') : milliseconds.format(value),
    time: (value: number) => timestamp.format(value * 1000),
    ratio: (value: number) => `${multiplier.format(value)}×`,
  }
}

function Status({ value }: { value: keyof typeof statuses }) {
  const { t } = useTranslation()
  const [label, color] = statuses[value]
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 text-xs font-medium',
        color
      )}
    >
      <span aria-hidden='true' className='size-1.5 rounded-full bg-current' />
      {t(label)}
    </span>
  )
}

function RefreshCountdown({ from }: { from: number }) {
  const { t } = useTranslation()
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [])
  const seconds = Math.max(
    0,
    Math.ceil((from + CHANNEL_STATUS_REFRESH_MS - now) / 1000)
  )
  return (
    <span
      className='text-muted-foreground text-xs tabular-nums'
      aria-hidden='true'
    >
      {t('Refresh in {{seconds}}s', { seconds })}
    </span>
  )
}

function HistoryBar({ health }: { health: ChannelGroupHealth }) {
  const { t } = useTranslation()
  const format = useFormats()
  return (
    <div>
      <div className='flex gap-0.5' aria-hidden='true'>
        {health.buckets.map((bucket) => {
          const rate = health.metrics_available ? bucket.success_rate : null
          return (
            <span
              key={bucket.bucket_start}
              title={`${format.time(bucket.bucket_start)} · ${format.rate(rate)}`}
              className={cn(
                'h-4 min-w-0 flex-1 rounded-full',
                rate == null ? 'bg-muted' : getSuccessRateDotClass(rate)
              )}
            />
          )
        })}
      </div>
      <div className='text-muted-foreground mt-1 flex justify-between text-[0.7rem]'>
        <span>{t('24 hours ago')}</span>
        <span>{t('Now')}</span>
      </div>
    </div>
  )
}

function HistoryTable({ health }: { health: ChannelGroupHealth }) {
  const { t } = useTranslation()
  const format = useFormats()
  if (!health.metrics_available) {
    return (
      <p className='text-muted-foreground py-3 text-sm'>
        {t('Group health metrics are unavailable.')}
      </p>
    )
  }
  return (
    <div className='overflow-hidden rounded-lg border'>
      <Table className='table-fixed [&_td]:whitespace-normal max-sm:[&_td]:text-xs [&_th]:whitespace-normal max-sm:[&_th]:text-xs'>
        <caption className='sr-only'>{t('Hourly history')}</caption>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Time')}</TableHead>
            <TableHead>{t('Success rate')}</TableHead>
            <TableHead>{t('Avg. first-token latency')}</TableHead>
            <TableHead>{t('Cache hit rate')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody className='[&>tr]:h-auto'>
          {[...health.buckets].reverse().map((bucket) => (
            <TableRow key={bucket.bucket_start}>
              <TableCell>{format.time(bucket.bucket_start)}</TableCell>
              <TableCell>{format.rate(bucket.success_rate)}</TableCell>
              <TableCell>{format.latency(bucket.average_ttft_ms)}</TableCell>
              <TableCell>{t('No data')}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function GroupCard({ group }: { group: ChannelStatusGroup }) {
  const { t } = useTranslation()
  const format = useFormats()
  const [range, setRange] = useState<Range>('24h')
  const { health } = group
  const metrics = [
    [
      t('Success rate'),
      format.rate(health.metrics_available ? health.success_rate : null),
    ],
    [
      t('Avg. first-token latency'),
      format.latency(health.metrics_available ? health.average_ttft_ms : null),
    ],
    [t('Cache hit rate'), t('No data')],
  ] as const
  return (
    <Card className='min-w-0 gap-3 p-3.5 sm:p-4' aria-label={group.label}>
      <Dialog
        onOpenChange={(open) => {
          if (open) setRange('24h')
        }}
      >
        <div className='flex items-start justify-between gap-2'>
          <div className='flex min-w-0 items-center gap-2'>
            <span
              aria-hidden='true'
              className='bg-muted flex size-8 shrink-0 items-center justify-center rounded-lg'
            >
              <MarketGroupIcon
                meta={group.catalog}
                platform={group.platform}
                size={18}
              />
            </span>
            <div className='min-w-0'>
              <h3 className='truncate text-sm leading-tight font-semibold'>
                {group.label}
              </h3>
              <div className='text-muted-foreground mt-1 text-xs'>
                {t(MARKET_PLATFORMS[group.platform])} ·{' '}
                {format.ratio(group.ratio)}
              </div>
            </div>
          </div>
          <Status value={healthStatus(health)} />
        </div>
        {group.description && (
          <p className='text-muted-foreground line-clamp-2 text-xs'>
            {t(group.description)}
          </p>
        )}
        <dl className='bg-muted/40 grid grid-cols-3 gap-2 rounded-lg p-2.5 text-xs'>
          {metrics.map(([label, value]) => (
            <div key={label}>
              <dt className='text-muted-foreground'>{label}</dt>
              <dd className='mt-1 font-semibold tabular-nums'>{value}</dd>
            </div>
          ))}
        </dl>
        <HistoryBar health={health} />
        <div className='mt-auto flex flex-wrap justify-between gap-2'>
          <DialogTrigger
            render={<Button variant='outline' size='sm' />}
            aria-label={t('Details for {{name}}', { name: group.label })}
          >
            <Activity aria-hidden='true' />
            {t('Details')}
          </DialogTrigger>
          {health.available && (
            <Link
              to='/keys'
              search={{ create: true, group: group.name }}
              className={buttonVariants({ size: 'sm' })}
              aria-label={t('Create API key for {{name}}', {
                name: group.label,
              })}
            >
              <KeyRound aria-hidden='true' />
              {t('Create API Key')}
            </Link>
          )}
        </div>
        <DialogContent className='sm:max-w-2xl'>
          <DialogHeader>
            <DialogTitle className='pr-8'>{group.label}</DialogTitle>
            <DialogDescription>
              {group.description ? t(group.description) : t('Hourly history')}
            </DialogDescription>
          </DialogHeader>
          <Status value={healthStatus(health)} />
          <div className='flex flex-wrap gap-1.5'>
            {ranges.map(([value, label]) => (
              <Button
                key={value}
                variant={value === range ? 'secondary' : 'outline'}
                size='sm'
                aria-pressed={value === range}
                onClick={() => setRange(value)}
              >
                {t(label)}
              </Button>
            ))}
          </div>
          {range === '24h' ? (
            <>
              <HistoryBar health={health} />
              <HistoryTable health={health} />
            </>
          ) : (
            <p className='text-muted-foreground rounded-lg border px-3 py-4 text-sm'>
              {t('Only the last 24 hours of group history are available.')}
            </p>
          )}
        </DialogContent>
      </Dialog>
    </Card>
  )
}

export function ChannelStatusPage() {
  const { t } = useTranslation()
  const { locale } = useFormats()
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [platform, setPlatform] = useState<MarketPlatform | 'all'>('all')
  const [sort, setSort] = useState<Sort>('ratio')
  const query = useChannelStatus(autoRefresh)
  const groups = query.isError ? [] : (query.data?.groups ?? [])
  const shown = sortGroups(
    groups.filter((group) => platform === 'all' || group.platform === platform),
    sort,
    locale
  )
  let overall: keyof typeof statuses = 'unknown'
  if (
    groups.some(
      ({ health }) => !health.available || healthStatus(health) === 'degraded'
    )
  ) {
    overall = 'degraded'
  } else if (
    groups.length > 0 &&
    !query.data?.incomplete &&
    groups.every(({ health }) => healthStatus(health) === 'healthy')
  ) {
    overall = 'healthy'
  }
  const refresh = () => {
    void query.refetch()
  }
  return (
    <SectionPageLayout stackActionsOnMobile>
      <SectionPageLayout.Title>{t('Channel status')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        {autoRefresh &&
          !query.isFetching &&
          !query.isError &&
          query.dataUpdatedAt > 0 && (
            <RefreshCountdown
              key={query.dataUpdatedAt}
              from={query.dataUpdatedAt}
            />
          )}
        <Button
          variant='outline'
          aria-pressed={autoRefresh}
          onClick={() => setAutoRefresh((value) => !value)}
        >
          {t('Auto-refresh')}
        </Button>
        <Button variant='outline' onClick={refresh} disabled={query.isFetching}>
          <RefreshCw
            aria-hidden='true'
            className={query.isFetching ? 'animate-spin' : undefined}
          />
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='space-y-3'>
          <div className='bg-muted/35 flex flex-wrap items-center justify-between gap-2 rounded-lg border px-3 py-2.5'>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Only aggregate service health is shown, without request counts, per-request records, or internal channel information.'
              )}
            </p>
            <Status value={overall} />
          </div>
          {query.isPending && <LoadingState />}
          {query.isError && (
            <ErrorState
              title={t('Could not load channel status')}
              onRetry={refresh}
            />
          )}
          {query.isSuccess && (
            <>
              {query.data?.incomplete && (
                <p role='status' className='text-muted-foreground text-xs'>
                  {t('Some health results are unavailable.')}
                </p>
              )}
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <div
                  className='flex flex-wrap gap-1.5'
                  role='group'
                  aria-label={t('Platform')}
                >
                  <Button
                    variant={platform === 'all' ? 'secondary' : 'outline'}
                    size='sm'
                    aria-pressed={platform === 'all'}
                    onClick={() => setPlatform('all')}
                  >
                    {t('All')} ({groups.length})
                  </Button>
                  {Object.entries(MARKET_PLATFORMS).map(([key, label]) => {
                    const value = key as MarketPlatform
                    const count = groups.filter(
                      (group) => group.platform === value
                    ).length
                    // Keep the selected platform operable even if it becomes empty.
                    return count || platform === value ? (
                      <Button
                        key={value}
                        variant={platform === value ? 'secondary' : 'outline'}
                        size='sm'
                        aria-pressed={platform === value}
                        onClick={() => setPlatform(value)}
                      >
                        <span aria-hidden='true' className='inline-flex'>
                          <MarketGroupIcon platform={value} size={13} />
                        </span>
                        {t(label)} ({count})
                      </Button>
                    ) : null
                  })}
                </div>
                <NativeSelect
                  size='sm'
                  aria-label={t('Sort groups')}
                  value={sort}
                  onChange={(event) => setSort(event.target.value as Sort)}
                >
                  {sorts.map(([value, label]) => (
                    <NativeSelectOption key={value} value={value}>
                      {t(label)}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
              {sort === 'cache' && (
                <p className='text-muted-foreground text-xs'>
                  {t('Group cache hit rates are not collected.')}
                </p>
              )}
              {shown.length ? (
                <div className='grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3'>
                  {shown.map((group) => (
                    <GroupCard key={group.name} group={group} />
                  ))}
                </div>
              ) : (
                <EmptyState
                  icon={Activity}
                  title={t('No groups to show')}
                  bordered
                />
              )}
            </>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

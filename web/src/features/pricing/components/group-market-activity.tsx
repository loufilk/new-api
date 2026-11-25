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
import { ArrowDown, ArrowUp, TrendingDown, TrendingUp } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import {
  marketRateDifference,
  validMarketRateChange,
  type RecentGroupActivity,
} from '../lib/group-market-activity'
import type { MarketRateChange } from '../types'

function MarketTime(props: { seconds: number }) {
  const stamp = formatTimestampToDate(props.seconds)
  return (
    <time
      dateTime={new Date(props.seconds * 1000).toISOString()}
      title={stamp}
      className='whitespace-nowrap tabular-nums'
    >
      {stamp}
    </time>
  )
}

// Detailed transition for the admin audit; customer quotes use compact badges.
export function MarketRateChangeLine(props: {
  change: MarketRateChange
  showTime?: boolean
}) {
  const { t } = useTranslation()
  const change = props.change
  if (!validMarketRateChange(change)) return null
  const rising = change.new_ratio > change.old_ratio
  const Trend = rising ? TrendingUp : TrendingDown
  return (
    <span className='inline-flex max-w-full min-w-0 flex-wrap items-baseline gap-x-1.5 gap-y-0.5 text-xs font-normal whitespace-normal'>
      <span
        className={cn(
          'inline-flex min-w-0 items-center gap-1 rounded px-1.5 py-0.5 font-medium [overflow-wrap:anywhere]',
          rising
            ? 'bg-amber-100 text-amber-950 dark:bg-amber-950 dark:text-amber-100'
            : 'bg-emerald-100 text-emerald-950 dark:bg-emerald-950 dark:text-emerald-100'
        )}
      >
        <Trend aria-hidden className='size-3 shrink-0' />
        <span>
          {t('Base multiplier')} {marketRateDifference(change)}×
        </span>
      </span>
      <span className='min-w-0 [overflow-wrap:anywhere] tabular-nums'>
        <span aria-hidden>
          {change.old_ratio}× → {change.new_ratio}×
        </span>
        <span className='sr-only'>
          {t('From {{old}}× to {{new}}×', {
            old: change.old_ratio,
            new: change.new_ratio,
          })}
        </span>
      </span>
      {props.showTime !== false && <MarketTime seconds={change.effective_at} />}
    </span>
  )
}

export function GroupMarketBadges(props: {
  activity?: RecentGroupActivity
  id?: string
}) {
  const { t } = useTranslation()
  if (!props.activity) return null
  const change = props.activity.change
  const rising = change && change.new_ratio > change.old_ratio
  const delta = change ? marketRateDifference(change) : undefined
  const Arrow = rising ? ArrowUp : ArrowDown
  return (
    <span
      id={props.id}
      className='inline-flex max-w-full min-w-0 flex-wrap items-center gap-1 text-start'
    >
      {props.activity.isNew && (
        <Badge variant='secondary' className='h-auto rounded px-1.5 py-0.5'>
          {t('New')}
        </Badge>
      )}
      {props.activity.isNew && delta && ' '}
      {delta && (
        <Badge
          className={cn(
            'h-auto max-w-full min-w-0 rounded px-1.5 py-0.5 whitespace-normal [overflow-wrap:anywhere] tabular-nums',
            rising
              ? 'bg-amber-100 text-amber-950 dark:bg-amber-950 dark:text-amber-100'
              : 'bg-emerald-100 text-emerald-950 dark:bg-emerald-950 dark:text-emerald-100'
          )}
        >
          <Arrow aria-hidden className='shrink-0' />
          <span aria-hidden className='min-w-0'>
            {delta.slice(1)}×
          </span>
          <span className='sr-only'>{delta}×</span>
        </Badge>
      )}
    </span>
  )
}

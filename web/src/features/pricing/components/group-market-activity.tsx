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
import { ChevronDown, TrendingDown, TrendingUp } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import {
  marketRateDifference,
  validMarketRateChange,
} from '../lib/group-market-activity'
import type { GroupMarketActivity, MarketRateChange } from '../types'

function MarketTime(props: { seconds: number; dateOnly?: boolean }) {
  const stamp = formatTimestampToDate(props.seconds)
  return (
    <time
      dateTime={new Date(props.seconds * 1000).toISOString()}
      title={stamp}
      className='whitespace-nowrap tabular-nums'
    >
      {props.dateOnly ? stamp.slice(0, 10) : stamp}
    </time>
  )
}

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

export function MarketNewBadge(props: { seconds: number }) {
  const { t } = useTranslation()
  return (
    <Badge variant='secondary' className='h-auto gap-1 rounded px-1.5 py-0.5'>
      {t('New')} <MarketTime seconds={props.seconds} dateOnly />
    </Badge>
  )
}

// The quote cell is the only expanded history surface. No inactive headings
// or placeholders are rendered for groups without recorded market events.
export function GroupQuoteActivity(props: {
  activity?: GroupMarketActivity
  label: string
}) {
  const { t } = useTranslation()
  const activity = props.activity
  if (!activity) return null
  const changes = activity.rate_changes
  const sameSecondEvents = new Map<string, number>()
  return (
    <div className='mt-1 flex min-w-0 flex-col items-start gap-1 text-xs'>
      {activity.first_configured_at && (
        <span className='text-muted-foreground flex flex-wrap items-center gap-1'>
          {activity.is_new ? (
            <MarketNewBadge seconds={activity.first_configured_at} />
          ) : (
            <>
              {t('Group added')}{' '}
              <MarketTime seconds={activity.first_configured_at} dateOnly />
            </>
          )}
        </span>
      )}
      {changes && changes.length > 0 && (
        <Collapsible className='group/history max-w-full min-w-0'>
          <CollapsibleTrigger
            render={
              <Button
                type='button'
                variant='outline'
                size='sm'
                className='h-auto min-h-8 max-w-full flex-wrap text-xs'
              />
            }
            aria-label={t('Multiplier history for {{group}}', {
              group: props.label,
            })}
          >
            {t('Multiplier history')}{' '}
            <span className='tabular-nums'>{changes.length}</span>
            <ChevronDown
              aria-hidden
              className='size-3 group-data-open/history:rotate-180'
            />
          </CollapsibleTrigger>
          <CollapsibleContent>
            <ol className='mt-2 max-h-64 min-w-0 space-y-2 overflow-y-auto border-s ps-2 text-start'>
              {changes.map((change) => {
                // Timestamps have one-second resolution: A→B→A→B can
                // legitimately contain two identical transitions.
                const event = `${change.effective_at}:${change.old_ratio}:${change.new_ratio}`
                const occurrence = (sameSecondEvents.get(event) ?? 0) + 1
                sameSecondEvents.set(event, occurrence)
                return (
                  <li key={`${event}:${occurrence}`}>
                    <MarketRateChangeLine change={change} />
                  </li>
                )
              })}
            </ol>
          </CollapsibleContent>
        </Collapsible>
      )}
    </div>
  )
}

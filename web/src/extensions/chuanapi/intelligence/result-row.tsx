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
import { Brain, ShieldCheck, Users } from 'lucide-react'
import { useEffect, useId, useRef, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestampRelative } from '@/lib/format'
import { cn } from '@/lib/utils'

import { ModelCapabilityBadge } from './capability-badge'
import { FingerprintReport } from './fingerprint-report'
import {
  effectiveFingerprintRuns,
  latestModelTestRuns,
  modelCapability,
} from './timeline'
import type { TestRun } from './types'

export function ResultRow(props: {
  runs: TestRun[]
  source: 'site' | 'user'
  onSelect: (run: TestRun) => void
  action?: ReactNode
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const id = useId()
  const recent = effectiveFingerprintRuns(props.runs)
  const latest = recent.at(-1)
  const fullResults = recent.filter((run) => modelCapability(run) === 'full')
  const latestFull = fullResults.at(-1)
  const fullCount = fullResults.length
  const timelineRef = useRef<HTMLOListElement>(null)
  useEffect(() => {
    const timeline = timelineRef.current
    if (timeline) {
      timeline.scrollLeft = Math.max(
        0,
        timeline.scrollWidth - timeline.clientWidth
      )
    }
  }, [latest?.id])
  const latestAttempt = latestModelTestRuns(props.runs).at(-1)
  const current = latestAttempt ?? latest
  const capability = current ? modelCapability(current) : null
  const fillClass = latestFull ? 'bg-emerald-500' : 'bg-emerald-500/30'
  let iconClass =
    'border-emerald-100 bg-emerald-50 text-emerald-600 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-400'
  if (capability === 'reduced') {
    iconClass =
      'border-red-100 bg-red-50 text-red-600 dark:border-red-900 dark:bg-red-950 dark:text-red-400'
  } else if (current && !capability && current.status !== 'running') {
    iconClass =
      'border-amber-100 bg-amber-50 text-amber-700 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-400'
  }
  const label =
    props.source === 'site'
      ? t('intelligence.siteResults')
      : t('fingerprint.userResults')
  const SourceIcon = props.source === 'site' ? ShieldCheck : Users
  return (
    <div className='grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-2 px-4 py-3 xl:grid-cols-[8rem_10rem_minmax(0,1fr)_5.5rem] xl:gap-x-5'>
      <div className='flex min-w-0 items-center gap-3'>
        <div
          aria-hidden
          className={cn(
            'flex size-11 shrink-0 items-center justify-center rounded-lg border',
            iconClass
          )}
        >
          <Brain className='size-6' />
        </div>
        <span className='text-muted-foreground flex min-w-0 items-center gap-1.5 text-xs'>
          <SourceIcon aria-hidden className='size-3.5 shrink-0' />
          {label}
        </span>
      </div>
      <div className='flex min-w-0 items-center gap-3 max-xl:justify-end'>
        <div className='flex min-w-0 flex-col items-start gap-1'>
          <span className='text-muted-foreground text-[11px]'>
            {t('fingerprint.latestResult')}
          </span>
          <ModelCapabilityBadge run={current} />
        </div>
        {current && (
          <time
            dateTime={new Date(current.created_at * 1000).toISOString()}
            title={new Date(current.created_at * 1000).toLocaleString(locale)}
            className='hidden shrink-0 flex-col gap-1 text-xs tabular-nums sm:flex'
          >
            <span>
              {new Date(current.created_at * 1000).toLocaleTimeString(locale, {
                hour: '2-digit',
                minute: '2-digit',
              })}
            </span>
            <span className='text-muted-foreground text-[11px]'>
              {formatTimestampRelative(current.created_at, 'seconds', locale)}
            </span>
          </time>
        )}
      </div>
      <div className='col-span-2 flex min-w-0 flex-col xl:col-span-1'>
        <div className='text-muted-foreground flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-[11px]'>
          <span>{t('fingerprint.recent', { count: recent.length })}</span>
          <span>
            {t('{{full}} full · {{reduced}} reduced', {
              full: formatNumber(fullCount, locale),
              reduced: formatNumber(recent.length - fullCount, locale),
            })}
          </span>
        </div>
        <ol
          ref={timelineRef}
          className='grid min-w-0 grid-cols-[repeat(60,minmax(2.75rem,1fr))] gap-0.5 overflow-x-auto overscroll-x-contain lg:pointer-fine:grid-cols-[repeat(60,minmax(0,1fr))]'
          aria-label={`${label} · ${t('intelligence.timeline')}`}
        >
          {Array.from({ length: 60 - recent.length }, (_, index) => (
            <li
              key={`filled-${index}`}
              aria-hidden
              className='flex h-11 items-center lg:pointer-fine:h-7'
            >
              <span
                className={cn(
                  'block h-5 w-full rounded-[5px] lg:pointer-fine:h-4',
                  fillClass
                )}
              />
            </li>
          ))}
          {recent.map((run) => (
            <li key={run.id} className='min-w-0'>
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Button
                      variant='ghost'
                      className='group/point h-11 w-full min-w-0 rounded-sm p-0 hover:bg-transparent focus-visible:ring-2 lg:pointer-fine:h-7'
                      aria-describedby={`${id}-${run.id}`}
                      onClick={() => props.onSelect(run)}
                      aria-label={`${modelCapability(run) === 'reduced' ? t('Reduced capability') : t('Full capability')} · ${new Date(run.created_at * 1000).toLocaleString(locale)} · ${t('fingerprint.openReport')}`}
                    />
                  }
                >
                  <span
                    aria-hidden
                    className={cn(
                      'block h-5 w-full rounded-[5px] transition-colors lg:pointer-fine:h-4',
                      modelCapability(run) === 'reduced'
                        ? 'bg-red-500 group-hover/point:bg-red-600'
                        : 'bg-emerald-500 group-hover/point:bg-emerald-600'
                    )}
                  />
                </TooltipTrigger>
                <TooltipContent
                  role='tooltip'
                  id={`${id}-${run.id}`}
                  className='bg-popover text-popover-foreground max-h-96 w-80 max-w-[calc(100vw-2rem)] overflow-y-auto rounded-xl border p-4 shadow-lg'
                >
                  <FingerprintReport run={run} compact />
                </TooltipContent>
              </Tooltip>
            </li>
          ))}
        </ol>
        <div className='text-muted-foreground/80 flex justify-between text-[10px]'>
          <span>{t('fingerprint.older')}</span>
          <span>{t('fingerprint.latest')}</span>
        </div>
        {latestFull && recent.length < 60 && (
          <p className='text-muted-foreground mt-1 text-[10px]'>
            {t('Filled from the latest full-capability result')}
          </p>
        )}
        {latestAttempt &&
          !recent.some((run) => run.id === latestAttempt.id) && (
            <Button
              variant='link'
              className='mt-1 h-auto justify-start p-0 text-xs whitespace-normal'
              onClick={() => props.onSelect(latestAttempt)}
            >
              {t('fingerprint.latestAttempt')} ·{' '}
              {t(`intelligence.status.${latestAttempt.status}`)}
            </Button>
          )}
      </div>
      <div className='col-span-2 flex justify-end empty:hidden xl:col-span-1 xl:empty:block'>
        {props.action}
      </div>
    </div>
  )
}

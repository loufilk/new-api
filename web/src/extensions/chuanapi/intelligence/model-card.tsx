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
import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { toIntlLocale } from '@/i18n/languages'

import { FingerprintReport } from './fingerprint-report'
import { effectiveFingerprintRuns } from './timeline'
import type { TestRun } from './types'

function ResultRow(props: {
  runs: TestRun[]
  label: string
  onSelect: (run: TestRun) => void
  action?: React.ReactNode
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const recent = effectiveFingerprintRuns(props.runs)
  const latest = recent.at(-1)
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
  const latestAttempt = [...props.runs].sort(
    (a, b) => b.created_at - a.created_at
  )[0]
  return (
    <div className='grid min-w-0 gap-4 p-4 lg:grid-cols-[10rem_minmax(0,1fr)_auto] lg:items-center'>
      <div className='flex flex-col gap-2'>
        <p className='text-muted-foreground text-sm'>{props.label}</p>
        <Badge variant='outline' className='w-fit'>
          {latest
            ? t('fingerprint.status.scored')
            : t('fingerprint.noEffective')}
        </Badge>
        {latest && (
          <time className='text-muted-foreground text-xs'>
            {new Date(latest.created_at * 1000).toLocaleString(locale)}
          </time>
        )}
      </div>
      <div className='flex min-w-0 flex-col gap-2'>
        <p className='text-muted-foreground text-xs'>
          {t('fingerprint.recent', { count: recent.length })}
        </p>
        <ol
          ref={timelineRef}
          className='grid min-w-0 grid-cols-[repeat(60,minmax(2.75rem,1fr))] gap-0.5 overflow-x-auto lg:pointer-fine:grid-cols-[repeat(60,minmax(0,1fr))]'
          aria-label={`${props.label} · ${t('intelligence.timeline')}`}
        >
          {Array.from({ length: 60 - recent.length }, (_, index) => (
            <li
              key={`empty-${index}`}
              aria-hidden
              className='bg-muted h-11 rounded-sm lg:pointer-fine:h-6'
            />
          ))}
          {recent.map((run) => (
            <li key={run.id} className='min-w-0'>
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Button
                      variant='ghost'
                      className='h-11 w-full min-w-0 rounded-sm bg-emerald-500 p-0 hover:bg-emerald-600 lg:pointer-fine:h-6'
                      aria-describedby={`fingerprint-preview-${run.id}`}
                      onClick={() => props.onSelect(run)}
                      aria-label={`${new Date(run.created_at * 1000).toLocaleString(locale)} · ${t('fingerprint.openReport')}`}
                    />
                  }
                />
                <TooltipContent
                  role='tooltip'
                  id={`fingerprint-preview-${run.id}`}
                  className='bg-popover text-popover-foreground max-h-96 w-80 max-w-[calc(100vw-2rem)] overflow-y-auto border p-4 shadow-lg'
                >
                  <FingerprintReport run={run} compact />
                </TooltipContent>
              </Tooltip>
            </li>
          ))}
        </ol>
        <div className='text-muted-foreground flex justify-between text-xs'>
          <span>{t('fingerprint.older')}</span>
          <span>{t('fingerprint.latest')}</span>
        </div>
        {latestAttempt &&
          !recent.some((run) => run.id === latestAttempt.id) && (
            <Button
              variant='link'
              className='h-auto justify-start p-0 text-xs whitespace-normal'
              onClick={() => props.onSelect(latestAttempt)}
            >
              {t('fingerprint.latestAttempt')} ·{' '}
              {latestAttempt.fingerprint_result
                ? t(
                    `fingerprint.status.${latestAttempt.fingerprint_result.status}`
                  )
                : t(`intelligence.status.${latestAttempt.status}`)}
            </Button>
          )}
      </div>
      {props.action}
    </div>
  )
}

export function ModelTestCard(props: {
  model: string
  group: string
  runs: TestRun[]
  siteRuns?: TestRun[]
  disabled: boolean
  estimating: boolean
  allowTest?: boolean
  onEstimate: () => void
  onSelect: (run: TestRun) => void
}) {
  const { t } = useTranslation()
  return (
    <article className='grid min-w-0 border-t md:grid-cols-[10rem_minmax(0,1fr)]'>
      <div className='flex min-w-0 flex-col justify-center gap-2 border-b p-4 md:border-r md:border-b-0'>
        <h3 className='font-semibold break-all'>{props.model}</h3>
        <span className='text-muted-foreground text-xs'>
          {t('fingerprint.modelFingerprint')}
        </span>
      </div>
      <div className='min-w-0 divide-y'>
        <ResultRow
          runs={props.siteRuns ?? []}
          label={t('intelligence.siteResults')}
          onSelect={props.onSelect}
        />
        <ResultRow
          runs={props.runs}
          label={t('fingerprint.myPrivateResults')}
          onSelect={props.onSelect}
          action={
            props.allowTest !== false && (
              <Button
                variant='outline'
                className='h-auto min-h-8 py-2 whitespace-normal'
                onClick={props.onEstimate}
                disabled={props.disabled || props.estimating}
              >
                {props.estimating
                  ? t('intelligence.estimating')
                  : t('fingerprint.selfTest')}
              </Button>
            )
          }
        />
      </div>
    </article>
  )
}

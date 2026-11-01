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
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { toIntlLocale } from '@/i18n/languages'

import { getTestDetail } from './api'
import { StaticArtwork } from './static-artwork'
import type { TestRun } from './types'

export function ModelTestCard(props: {
  model: string
  group: string
  runs: TestRun[]
  disabled: boolean
  estimating: boolean
  onEstimate: () => void
  onSelect: (run: TestRun) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const recent = [...props.runs]
    .sort((a, b) => b.created_at - a.created_at)
    .slice(0, 60)
  const valid = recent.filter(
    (run) => run.status === 'completed' && run.assessment === 'renderable'
  )
  const latest = valid[0]
  const running = recent.some((run) => run.status === 'running')
  const detail = useQuery({
    queryKey: ['model-tests', 'detail', latest?.id, latest?.status],
    queryFn: ({ signal }) => getTestDetail(latest?.id ?? '', signal),
    enabled: !!latest,
    staleTime: Infinity,
    retry: false,
    meta: { errorToast: false },
  })
  return (
    <Card className='min-w-0'>
      <CardHeader>
        <CardTitle>
          <h3 className='break-all'>{props.model}</h3>
        </CardTitle>
        <Badge variant='outline'>
          {running
            ? t('intelligence.generating')
            : t('intelligence.visualTask')}
        </Badge>
      </CardHeader>
      <CardContent className='flex flex-col gap-3'>
        {detail.data?.artwork_html ? (
          <StaticArtwork html={detail.data.artwork_html} thumbnail />
        ) : (
          <EmptyState
            className='min-h-40'
            title={t('intelligence.noArtwork')}
          />
        )}
        <p className='text-muted-foreground text-xs'>
          {t('intelligence.latestValid')}:{' '}
          {latest
            ? new Date(latest.completed_at * 1000).toLocaleString(locale)
            : '—'}
        </p>
        <p className='text-sm'>
          {t('intelligence.validCount', {
            valid: valid.length,
            count: recent.length,
          })}
        </p>
        <ol
          className='flex flex-wrap gap-1'
          aria-label={t('intelligence.timeline')}
        >
          {Array.from({ length: 60 - recent.length }, (_, index) => (
            <li
              key={`empty-${index}`}
              aria-hidden
              className='bg-muted h-6 w-2 rounded-sm'
            />
          ))}
          {[...recent].reverse().map((run) => (
            <li key={run.id}>
              <Button
                variant={
                  run.assessment === 'renderable' && run.status === 'completed'
                    ? 'secondary'
                    : 'outline'
                }
                className='h-6 w-6 px-0'
                onClick={() => props.onSelect(run)}
                aria-label={`${new Date(run.created_at * 1000).toLocaleString(locale)} · ${t(`intelligence.status.${run.status}`)}`}
                title={t(`intelligence.status.${run.status}`)}
              >
                <span aria-hidden>{run.status === 'running' ? '·' : ''}</span>
              </Button>
            </li>
          ))}
        </ol>
        <p className='text-muted-foreground text-xs'>
          {t('intelligence.timelineHint')}
        </p>
      </CardContent>
      <CardFooter className='flex flex-wrap gap-2'>
        <Button
          className='h-auto min-h-8 max-w-full py-2 whitespace-normal'
          onClick={props.onEstimate}
          disabled={props.disabled || props.estimating}
        >
          {props.estimating
            ? t('intelligence.estimating')
            : t('intelligence.reviewBudget')}
        </Button>
        {latest && (
          <Button
            variant='outline'
            className='h-auto min-h-8 max-w-full py-2 whitespace-normal'
            onClick={() => props.onSelect(latest)}
          >
            {t('intelligence.viewLatest')}
          </Button>
        )}
      </CardFooter>
    </Card>
  )
}

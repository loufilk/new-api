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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { toIntlLocale } from '@/i18n/languages'

import { Artwork } from './artwork'
import { resultStatusKey, resultTime } from './result-state'
import type { MonitorResult, MonitorTarget } from './types'

export function TargetCard(props: {
  target: MonitorTarget
  results: MonitorResult[]
  onSelect: (result: MonitorResult) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [expanded, setExpanded] = useState(false)
  const latestArtwork = props.results.find(
    (result) => result.benchmark === 'pelican'
  )
  const history = expanded ? props.results : props.results.slice(0, 4)

  return (
    <Card className='min-w-0 self-start'>
      <CardHeader>
        <CardDescription>
          {props.target.name ||
            t('monitor.route', { number: props.target.number })}
        </CardDescription>
        <CardTitle>
          <h2 className='text-xl break-all'>{props.target.model}</h2>
        </CardTitle>
      </CardHeader>
      <CardContent className='space-y-4'>
        {latestArtwork?.status === 'ready' ? (
          <>
            <Artwork
              key={`${latestArtwork.id}:${latestArtwork.revision}`}
              result={latestArtwork}
              thumbnail
            />
            <Button
              variant='outline'
              className='w-full'
              onClick={() => props.onSelect(latestArtwork)}
            >
              {t('monitor.viewArtwork')}
            </Button>
          </>
        ) : (
          <EmptyState title={t('monitor.noArtwork')} className='min-h-40' />
        )}
        <h3 className='text-sm font-medium'>{t('monitor.history')}</h3>
        {history.length === 0 && (
          <p className='text-muted-foreground text-sm'>
            {t('monitor.noResults')}
          </p>
        )}
        <div
          className='max-h-80 space-y-2 overflow-y-auto'
          aria-label={t('monitor.history')}
        >
          {history.map((result) => (
            <Button
              key={result.id}
              variant='outline'
              className='h-auto w-full justify-between gap-2 px-3 py-2 text-start'
              onClick={() => props.onSelect(result)}
            >
              <span className='min-w-0'>
                <span className='block'>
                  {t(
                    result.benchmark === 'pelican'
                      ? 'monitor.visual'
                      : 'monitor.logic'
                  )}
                </span>
                <span className='text-muted-foreground text-xs'>
                  {resultTime(result.started_at, locale)}
                </span>
              </span>
              <Badge
                variant={
                  resultStatusKey(result) === 'monitor.passed'
                    ? 'default'
                    : 'secondary'
                }
              >
                {t(resultStatusKey(result))}
              </Badge>
            </Button>
          ))}
        </div>
        {props.results.length > 4 && (
          <Button
            variant='ghost'
            className='w-full'
            aria-expanded={expanded}
            onClick={() => setExpanded(!expanded)}
          >
            {t(expanded ? 'monitor.collapseHistory' : 'monitor.allHistory')}
          </Button>
        )}
      </CardContent>
    </Card>
  )
}

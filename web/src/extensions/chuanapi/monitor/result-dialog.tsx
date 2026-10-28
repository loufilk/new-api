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

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { getMonitorDetail } from './api'
import { Artwork } from './artwork'
import { resultStatusKey, resultTime } from './result-state'
import type { MonitorResult } from './types'

export function ResultDialog(props: {
  result: MonitorResult
  onClose: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const result = props.result
  const detail = useQuery({
    queryKey: [
      'model-monitor-result',
      result.id,
      result.revision,
      result.status,
      result.quality_status,
      result.completed_at,
    ],
    queryFn: ({ signal }) => getMonitorDetail(result.id, signal),
    staleTime: 30_000,
    retry: false,
    meta: { errorToast: false },
  })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t(
        result.benchmark === 'pelican' ? 'monitor.visual' : 'monitor.logic'
      )}
      description={resultTime(result.started_at, locale)}
      contentClassName='sm:max-w-3xl'
    >
      <div className='space-y-5'>
        <Badge variant='secondary'>{t(resultStatusKey(result))}</Badge>
        {result.benchmark === 'pelican' && result.status === 'ready' && (
          <Artwork key={`${result.id}:${result.revision}`} result={result} />
        )}
        {result.quality_reason && (
          <p className='text-muted-foreground text-sm break-words whitespace-pre-wrap'>
            {result.quality_reason}
          </p>
        )}
        {detail.isPending && (
          <LoadingState message={t('monitor.loadingDetails')} />
        )}
        {detail.isError && (
          <ErrorState
            title={t('monitor.detailsUnavailable')}
            onRetry={() => void detail.refetch()}
          />
        )}
        {detail.data && (
          <>
            {detail.data.stale && (
              <Alert>
                <AlertDescription>{t('monitor.staleDetails')}</AlertDescription>
              </Alert>
            )}
            <section className='space-y-2'>
              <h3 className='font-medium'>{t('monitor.question')}</h3>
              <p className='text-sm break-words whitespace-pre-wrap'>
                {detail.data.data.prompt || t('monitor.noDetails')}
              </p>
            </section>
            {result.expected_answer !== undefined && (
              <p className='text-sm'>
                {t('monitor.expectedAnswer', {
                  answer: formatNumber(result.expected_answer, locale),
                })}
              </p>
            )}
            {result.benchmark === 'candy' && (
              <section className='space-y-2'>
                <h3 className='font-medium'>{t('monitor.answer')}</h3>
                <p className='text-sm break-words whitespace-pre-wrap'>
                  {detail.data.data.answer || t('monitor.noDetails')}
                </p>
              </section>
            )}
            {detail.data.data.reference_reason && (
              <section className='space-y-2'>
                <h3 className='font-medium'>{t('monitor.reason')}</h3>
                <p className='text-sm break-words whitespace-pre-wrap'>
                  {detail.data.data.reference_reason}
                </p>
              </section>
            )}
          </>
        )}
      </div>
    </Dialog>
  )
}

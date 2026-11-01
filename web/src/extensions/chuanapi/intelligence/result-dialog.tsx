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

import { getTestDetail } from './api'
import { modelTestFailureKeys } from './errors'
import { StaticArtwork } from './static-artwork'
import type { TestRun } from './types'

export function TestResultDialog(props: { run: TestRun; onClose: () => void }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const query = useQuery({
    queryKey: ['model-tests', 'detail', props.run.id, props.run.status],
    queryFn: ({ signal }) => getTestDetail(props.run.id, signal),
    retry: false,
    meta: { errorToast: false },
  })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={props.run.model}
      description={`${props.run.group} · ${props.run.benchmark_id}`}
      contentClassName='sm:max-w-4xl'
    >
      <div className='flex flex-col gap-4'>
        <Badge variant='secondary'>
          {t(`intelligence.status.${props.run.status}`)}
        </Badge>
        {modelTestFailureKeys[props.run.error_code] && (
          <Alert>
            <AlertDescription>
              {t(modelTestFailureKeys[props.run.error_code])}
            </AlertDescription>
          </Alert>
        )}
        <p className='text-muted-foreground text-xs'>
          {new Date(props.run.created_at * 1000).toLocaleString(locale)}
        </p>
        <Alert>
          <AlertDescription>{t('intelligence.staticOnly')}</AlertDescription>
        </Alert>
        {query.isPending && <LoadingState />}
        {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
        {query.data && (
          <>
            {query.data.artwork_html ? (
              <StaticArtwork html={query.data.artwork_html} />
            ) : (
              <p>{t('intelligence.noArtwork')}</p>
            )}
            <h3 className='font-semibold'>{t('monitor.question')}</h3>
            <pre className='text-muted-foreground text-sm break-words whitespace-pre-wrap'>
              {query.data.prompt}
            </pre>
          </>
        )}
      </div>
    </Dialog>
  )
}

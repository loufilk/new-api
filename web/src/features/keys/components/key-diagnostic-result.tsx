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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatNumber } from '@/lib/format'

import {
  type DiagnosticRun,
  diagnosticInProgress,
  diagnosticQuestionLabels,
  diagnosticStatusLabels,
} from '../lib/key-diagnostic'
import { diagnosticSvgImage } from '../lib/key-diagnostic-svg'

const candyLabels = {
  correct: 'Correct answer',
  incorrect: 'Incorrect answer',
  unrecognized: 'Answer not recognized',
} as const

export function KeyDiagnosticResult({ run }: { run: DiagnosticRun }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const artwork = useMemo(
    () =>
      run.status === 'completed' &&
      run.question === 'pelican-svg-v1' &&
      run.answer
        ? diagnosticSvgImage(run.answer)
        : null,
    [run.status, run.question, run.answer]
  )
  const pending = diagnosticInProgress(run.status)
  const usage = run.usage
  const usageConfirmed =
    usage?.prompt_tokens !== undefined || usage?.completion_tokens !== undefined
  const charge = run.billing
  let displayedCharge = t('Settlement not confirmed')
  if (charge?.status === 'refunded') {
    displayedCharge = t('Generation charge refunded')
  }
  if (charge?.status === 'settled' && charge.quota !== undefined) {
    displayedCharge = formatQuotaWithCurrency(charge.quota, {
      locale,
      abbreviate: false,
    })
  }
  const isCandy = run.question === 'candy-logic-v1'
  const verdict =
    isCandy && run.status === 'completed' ? run.candy_result : undefined
  return (
    <section
      className='space-y-3 rounded-lg border p-3'
      aria-label={t('Test result')}
    >
      <p role='status' className='text-sm font-medium'>
        {t('Status')}: {t(diagnosticStatusLabels[run.status])}
        {pending && (
          <span className='text-muted-foreground block font-normal'>
            {t(
              'The test is running in the background. You can close this window and return to it later.'
            )}
          </span>
        )}
      </p>
      {run.status === 'unknown' && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'The outcome is not confirmed. Do not treat it as a failed or free test.'
          )}
        </p>
      )}
      {run.status === 'failed' || run.status === 'intercepted' ? (
        <p className='text-sm'>
          {t('The request did not return a test answer.')}
        </p>
      ) : null}
      <dl className='grid grid-cols-[minmax(0,1fr)_minmax(0,2fr)] gap-x-3 gap-y-1 text-xs'>
        {run.model && (
          <>
            <dt>{t('Model')}</dt>
            <dd className='break-all'>{run.model}</dd>
          </>
        )}
        {run.question && (
          <>
            <dt>{t('Test question')}</dt>
            <dd>{t(diagnosticQuestionLabels[run.question])}</dd>
          </>
        )}
        {run.elapsed_ms !== undefined && (
          <>
            <dt>{t('Elapsed time')}</dt>
            <dd>{formatNumber(run.elapsed_ms, locale)} ms</dd>
          </>
        )}
        {run.http_status !== undefined && run.http_status > 0 && (
          <>
            <dt>{t('HTTP status')}</dt>
            <dd>{run.http_status}</dd>
          </>
        )}
        <dt>{t('Request ID')}</dt>
        <dd className='break-all'>{run.request_id || '—'}</dd>
        <dt>{t('Test ID')}</dt>
        <dd className='break-all'>{run.id}</dd>
        {usage?.prompt_tokens !== undefined && (
          <>
            <dt>{t('Input tokens')}</dt>
            <dd>{formatNumber(usage.prompt_tokens, locale)}</dd>
          </>
        )}
        {usage?.completion_tokens !== undefined && (
          <>
            <dt>{t('Output tokens')}</dt>
            <dd>{formatNumber(usage.completion_tokens, locale)}</dd>
          </>
        )}
        {!usageConfirmed && (
          <>
            <dt>{t('Token usage')}</dt>
            <dd>{t('Not confirmed')}</dd>
          </>
        )}
        <dt>{t('Generation charge')}</dt>
        <dd>{displayedCharge}</dd>
      </dl>
      {charge && charge.status !== 'unconfirmed' && (
        <p className='text-muted-foreground text-xs'>
          {t('Generation settlement only. Other fees are not confirmed here.')}
        </p>
      )}
      {verdict && (
        <p className='text-sm font-medium'>{t(candyLabels[verdict])}</p>
      )}
      {isCandy && run.status === 'completed' && !verdict && (
        <p className='text-sm'>{t('Answer not recognized')}</p>
      )}
      {artwork && (
        <img
          src={artwork}
          alt={t('Isolated SVG preview')}
          className='bg-background max-h-80 w-full rounded-md border object-contain'
        />
      )}
      {run.answer && (
        <>
          {run.status === 'completed' &&
            run.question === 'pelican-svg-v1' &&
            !artwork && (
              <p className='text-muted-foreground text-sm'>
                {t(
                  'A safe SVG preview is not available. The original reply is shown below.'
                )}
              </p>
            )}
          <div className='space-y-1'>
            <div className='flex items-center justify-between gap-2'>
              <p className='text-sm font-medium'>{t('Model reply')}</p>
              {run.question === 'pelican-svg-v1' && (
                <CopyButton
                  value={run.answer}
                  variant='outline'
                  size='sm'
                  aria-label={t('Copy original source')}
                >
                  {t('Copy original source')}
                </CopyButton>
              )}
            </div>
            <pre className='bg-muted max-h-64 overflow-auto rounded-md p-3 text-xs break-words whitespace-pre-wrap'>
              {run.answer}
            </pre>
          </div>
        </>
      )}
    </section>
  )
}

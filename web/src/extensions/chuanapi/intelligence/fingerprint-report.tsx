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
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { toIntlLocale } from '@/i18n/languages'

import { ModelCapabilityBadge } from './capability-badge'
import { formatModelTestReason } from './report-reason'
import type { TestRun } from './types'

export function FingerprintReport(props: { run: TestRun; compact?: boolean }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const percent = new Intl.NumberFormat(locale, {
    style: 'percent',
    maximumFractionDigits: 2,
  })
  const result = props.run.fingerprint_result
  return (
    <div className='flex min-w-0 flex-col gap-3 text-sm'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <time dateTime={new Date(props.run.created_at * 1000).toISOString()}>
          {new Date(props.run.created_at * 1000).toLocaleString(locale)}
        </time>
        <div className='flex flex-wrap items-center gap-1.5'>
          <ModelCapabilityBadge run={props.run} />
          {result && result.status !== 'scored' && (
            <Badge variant='outline'>
              {t(`fingerprint.status.${result.status}`)}
            </Badge>
          )}
        </div>
      </div>
      {!result ? (
        <p>{t('fingerprint.noReport')}</p>
      ) : (
        <>
          <h4 className='font-semibold'>{t('fingerprint.candidates')}</h4>
          {result.top_candidates.length ? (
            <ol className='flex flex-col gap-2'>
              {result.top_candidates
                .slice(0, props.compact ? 3 : 5)
                .map((candidate) => (
                  <li
                    key={candidate.model}
                    className='flex items-start justify-between gap-3'
                  >
                    <span className='min-w-0 break-all'>
                      {candidate.display_name || candidate.model}
                    </span>
                    <span className='shrink-0 tabular-nums'>
                      {candidate.probability === null
                        ? t('fingerprint.unavailable')
                        : percent.format(candidate.probability)}
                    </span>
                  </li>
                ))}
            </ol>
          ) : (
            <p>{t('fingerprint.noCandidates')}</p>
          )}
          <p className='text-muted-foreground text-xs'>
            {t('fingerprint.probabilityScope')}
          </p>
          <p>
            {t('fingerprint.sampleCount', {
              accepted: result.used_outputs,
              total: result.sample_quality.length,
            })}
          </p>
          {!props.compact && (
            <ul className='flex flex-col gap-2'>
              {result.sample_quality.map((sample) => (
                <li key={sample.challenge_id} className='rounded-md border p-2'>
                  <span className='font-medium'>{sample.challenge_id}</span> ·{' '}
                  {t(
                    sample.accepted
                      ? 'fingerprint.accepted'
                      : 'fingerprint.rejected'
                  )}
                  <p>
                    {t('fingerprint.sampleNumbers', {
                      count: sample.parsed_numbers,
                      minimum: sample.minimum_numbers,
                      expected: sample.expected_count,
                    })}
                  </p>
                  {sample.truncated && <p>{t('fingerprint.truncated')}</p>}
                  {sample.out_of_range_count > 0 && (
                    <p>
                      {t('fingerprint.outOfRange', {
                        count: sample.out_of_range_count,
                      })}
                    </p>
                  )}
                  {sample.reason && (
                    <p className='text-muted-foreground break-all'>
                      {formatModelTestReason(sample.reason, t)}
                    </p>
                  )}
                </li>
              ))}
            </ul>
          )}
          {result.reason && (
            <p className='break-all'>
              {formatModelTestReason(result.reason, t)}
            </p>
          )}
          <dl className='grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs'>
            <dt>{t('fingerprint.dataVersion')}</dt>
            <dd className='break-all'>{result.data_version}</dd>
            <dt>{t('fingerprint.algorithmVersion')}</dt>
            <dd className='break-all'>{result.sdk_commit}</dd>
            {!props.compact && (
              <>
                <dt>{t('fingerprint.dataHash')}</dt>
                <dd className='break-all'>{result.data_sha256}</dd>
              </>
            )}
          </dl>
        </>
      )}
      <p className='text-muted-foreground text-xs'>
        {t('fingerprint.disclaimer')}
      </p>
    </div>
  )
}

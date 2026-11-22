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

import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'

import type { TokenGroupDetail } from '../types'

export function ApiKeyGroupHealth(props: { detail?: TokenGroupDetail }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const rate = props.detail?.success_rate
  const validRate =
    typeof rate === 'number' && Number.isFinite(rate) && rate >= 0 && rate <= 1
  const history = Array.from({ length: 24 }, (_, index) => ({
    hour: index - 24,
    rate: props.detail?.history?.[index] ?? null,
  }))
  return (
    <span className='mt-1.5 block'>
      <span className='flex h-3 gap-0.5' aria-hidden='true'>
        {history.map((bucket) => {
          const sample = bucket.rate
          const known =
            typeof sample === 'number' &&
            Number.isFinite(sample) &&
            sample >= 0 &&
            sample <= 1
          return (
            <span
              key={bucket.hour}
              className={cn(
                'min-w-0 flex-1 rounded-[2px] bg-muted',
                known && sample >= 0.95 && 'bg-emerald-500',
                known && sample > 0 && sample < 0.95 && 'bg-amber-400',
                known && sample === 0 && 'bg-red-400'
              )}
            />
          )
        })}
      </span>
      <span className='text-muted-foreground mt-1 flex justify-between gap-1 text-[10px]'>
        <span>{t('24 hours ago')}</span>
        <span>
          {validRate
            ? t('{{rate}}% success rate', {
                rate: formatNumber(rate * 100, locale),
              })
            : t('No performance data')}
        </span>
        <span>{t('Now')}</span>
      </span>
    </span>
  )
}

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

import { Card } from '@/components/ui/card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type {
  Measurement,
  PublicLine,
  usePublicLineHealth,
} from './use-public-line-health'

type LineSort =
  | 'default'
  | 'success_rate'
  | 'average_latency_ms'
  | 'cache_hit_rate'

function sortPublicLines(a: PublicLine, b: PublicLine, sort: LineSort): number {
  if (sort !== 'default') {
    const left = a[sort]
    const right = b[sort]
    if (left == null && right != null) return 1
    if (left != null && right == null) return -1
    if (left != null && right != null && left !== right) {
      return sort === 'average_latency_ms' ? left - right : right - left
    }
  }
  return a.sort_order - b.sort_order || a.label.localeCompare(b.label)
}

function HealthMeasures({ data }: { data: Measurement }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const percent = new Intl.NumberFormat(locale, {
    style: 'percent',
    maximumFractionDigits: 0,
  })
  return (
    <dl className='grid grid-cols-3 gap-2 text-sm'>
      <div>
        <dt className='text-muted-foreground'>{t('Success rate')}</dt>
        <dd>
          {data.success_rate == null
            ? '–'
            : percent.format(data.success_rate / 100)}
        </dd>
      </div>
      <div>
        <dt className='text-muted-foreground'>
          {t('Average request latency')}
        </dt>
        <dd>
          {data.average_latency_ms == null
            ? '–'
            : `${formatNumber(data.average_latency_ms, locale)} ms`}
        </dd>
      </div>
      <div>
        <dt className='text-muted-foreground'>{t('Cache hit rate')}</dt>
        <dd>
          {data.cache_hit_rate == null
            ? '–'
            : percent.format(data.cache_hit_rate / 100)}
        </dd>
      </div>
    </dl>
  )
}

export function PublicLineHealth({
  query,
}: {
  query: ReturnType<typeof usePublicLineHealth>
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [vendor, setVendor] = useState('')
  const [sort, setSort] = useState<LineSort>('default')
  const lines = query.isError ? [] : (query.data?.lines ?? [])
  const vendors = [...new Set(lines.map((line) => line.vendor || ''))]
    .filter(Boolean)
    .sort((a, b) => a.localeCompare(b, locale))
  const selectedVendor = vendors.includes(vendor) ? vendor : ''
  const visible = lines
    .filter((line) => !selectedVendor || line.vendor === selectedVendor)
    .sort((a, b) => sortPublicLines(a, b, sort))
  const dateLabel = (ts: number) => new Date(ts * 1000).toLocaleString(locale)
  return (
    <section aria-label={t('Public line performance')} className='space-y-4'>
      <h2 className='text-xl font-semibold'>{t('Public line performance')}</h2>
      {query.isError && (
        <p role='alert'>{t('Unable to load public line metrics.')}</p>
      )}
      {query.isPending && <p role='status'>{t('Loading...')}</p>}
      {!query.isError && query.data && lines.length === 0 && (
        <p role='status'>{t('No public line metrics')}</p>
      )}
      {!query.isError && query.data && lines.length > 0 && (
        <>
          <p className='text-muted-foreground text-sm'>
            {t('24 completed hours')}: {dateLabel(query.data.window_start)} ·{' '}
            {dateLabel(query.data.window_end)}
            <br />
            {t(
              'Latency is total request time, not time to first token. Cache rate is based on successful requests reporting cache usage.'
            )}
          </p>
          <div className='flex flex-wrap items-center gap-3'>
            <label className='grid gap-1 text-sm'>
              {t('Vendor')}
              <select
                className='bg-background h-9 rounded-md border px-2'
                value={selectedVendor}
                onChange={(event) => setVendor(event.currentTarget.value)}
              >
                <option value=''>{t('All vendors')}</option>
                {vendors.map((name) => (
                  <option key={name} value={name}>
                    {name}
                  </option>
                ))}
              </select>
            </label>
            <label className='grid gap-1 text-sm'>
              {t('Sort by')}
              <select
                className='bg-background h-9 rounded-md border px-2'
                value={sort}
                onChange={(event) => {
                  const value = event.currentTarget.value
                  if (
                    value === 'default' ||
                    value === 'success_rate' ||
                    value === 'average_latency_ms' ||
                    value === 'cache_hit_rate'
                  ) {
                    setSort(value)
                  }
                }}
              >
                <option value='default'>{t('Default order')}</option>
                <option value='success_rate'>{t('Success rate')}</option>
                <option value='average_latency_ms'>
                  {t('Average request latency')}
                </option>
                <option value='cache_hit_rate'>{t('Cache hit rate')}</option>
              </select>
            </label>
          </div>
          <div className='grid gap-3 md:grid-cols-2'>
            {visible.map((line) => (
              <Card key={line.id} className='min-w-0 gap-3 p-4'>
                <div>
                  <h3 className='font-medium break-words'>{line.label}</h3>
                  <p className='text-muted-foreground text-sm break-words'>
                    {line.representative_model}
                    {line.vendor && ` · ${line.vendor}`} ·{' '}
                    {formatNumber(line.ratio, locale)}×
                  </p>
                </div>
                <HealthMeasures data={line} />
                <details className='border-t pt-2'>
                  <summary className='cursor-pointer text-sm'>
                    {t('Hourly history')}
                  </summary>
                  <ol className='max-h-72 divide-y overflow-y-auto'>
                    {line.buckets.map((bucket) => (
                      <li key={bucket.bucket_start} className='py-2'>
                        <p className='text-muted-foreground mb-1 text-xs'>
                          {dateLabel(bucket.bucket_start)}
                        </p>
                        <HealthMeasures data={bucket} />
                      </li>
                    ))}
                  </ol>
                </details>
              </Card>
            ))}
          </div>
        </>
      )}
    </section>
  )
}

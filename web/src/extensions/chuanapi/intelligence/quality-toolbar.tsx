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
import { Search } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Input } from '@/components/ui/input'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { QualityTarget, QualityTargetSummary } from './target-summary'

export function QualityToolbar(props: {
  targets: QualityTargetSummary<QualityTarget>[]
  pending: boolean
  search: string
  onSearch: (value: string) => void
  state: string
  onState: (value: string) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const summaries = [
    { title: 'Monitored targets', count: props.targets.length },
    {
      title: 'Recent full-capability results',
      count: props.targets.filter((target) => target.capability === 'full')
        .length,
    },
    {
      title: 'Targets needing review',
      count: props.targets.filter((target) => target.capability === 'reduced')
        .length,
    },
    {
      title: 'Awaiting a valid result',
      count: props.targets.filter((target) => target.capability === null)
        .length,
    },
  ]
  return (
    <section className='space-y-4' aria-label={t('Quality overview')}>
      <div className='grid grid-cols-2 gap-3 lg:grid-cols-4'>
        {summaries.map((item) => (
          <div key={item.title} className='bg-card rounded-xl border p-4'>
            <p className='text-muted-foreground text-xs'>{t(item.title)}</p>
            <p className='mt-2 text-2xl font-semibold tabular-nums'>
              {props.pending ? '—' : formatNumber(item.count, locale)}
            </p>
          </div>
        ))}
      </div>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <Tabs
          value={props.state}
          onValueChange={(value) => props.onState(String(value))}
          className='max-w-full overflow-x-auto'
        >
          <TabsList>
            <TabsTrigger value='all'>{t('All results')}</TabsTrigger>
            <TabsTrigger value='full'>{t('Full capability')}</TabsTrigger>
            <TabsTrigger value='reduced'>{t('Reduced capability')}</TabsTrigger>
            <TabsTrigger value='unknown'>{t('No valid result')}</TabsTrigger>
          </TabsList>
        </Tabs>
        <div className='relative w-full sm:w-72'>
          <Search
            className='text-muted-foreground absolute top-2.5 left-3 size-4'
            aria-hidden
          />
          <Input
            value={props.search}
            onChange={(event) => props.onSearch(event.target.value)}
            aria-label={t('Search quality targets')}
            placeholder={t('Search model or group')}
            className='pl-9'
          />
        </div>
      </div>
    </section>
  )
}

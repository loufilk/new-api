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
import { ChevronRight } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { MarketGroupIcon } from '@/features/pricing/components/market-group-icon'
import type { MarketGroup } from '@/features/pricing/types'

import { catalogPlatforms } from './group-catalog-platforms'

const pageSize = 30

export function GroupCatalogList(props: {
  groups: Record<string, MarketGroup>
  baseRates: Record<string, number>
  lockedGroup?: string
  onOpen: (group: string, trigger: HTMLButtonElement) => void
}) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const [platform, setPlatform] = useState('all')
  const [access, setAccess] = useState('all')
  const [limit, setLimit] = useState(pageSize)
  const term = query.trim().toLowerCase()
  const groups = Object.keys(props.baseRates)
    .filter((id) => {
      const entry = Object.hasOwn(props.groups, id)
        ? props.groups[id]
        : undefined
      return (
        (platform === 'all' || platform === (entry?.platform ?? '')) &&
        (access === 'all' || (access === 'exclusive') === !!entry?.exclusive) &&
        (!term ||
          id.toLowerCase().includes(term) ||
          entry?.label?.toLowerCase().includes(term))
      )
    })
    .sort()
  return (
    <div className='@container space-y-4'>
      <div className='grid min-w-0 gap-3 @min-[540px]:grid-cols-[minmax(0,2fr)_repeat(2,minmax(0,1fr))]'>
        <label className='block min-w-0 space-y-1.5 text-sm font-medium'>
          <span>{t('Search groups')}</span>
          <Input
            value={query}
            placeholder={t('Display name or group ID')}
            onChange={(e) => {
              setQuery(e.target.value)
              setLimit(pageSize)
            }}
          />
        </label>
        <label className='block min-w-0 space-y-1.5 text-sm font-medium'>
          <span>{t('Platform')}</span>
          <NativeSelect
            className='h-9 w-full'
            value={platform}
            onChange={(e) => {
              setPlatform(e.target.value)
              setLimit(pageSize)
            }}
          >
            <NativeSelectOption value='all'>{t('All')}</NativeSelectOption>
            {catalogPlatforms.map(([key, title]) => (
              <NativeSelectOption key={key} value={key}>
                {t(title)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </label>
        <label className='block min-w-0 space-y-1.5 text-sm font-medium'>
          <span>{t('Group access')}</span>
          <NativeSelect
            className='h-9 w-full'
            value={access}
            onChange={(e) => {
              setAccess(e.target.value)
              setLimit(pageSize)
            }}
          >
            <NativeSelectOption value='all'>{t('All')}</NativeSelectOption>
            <NativeSelectOption value='normal'>
              {t('Not exclusive')}
            </NativeSelectOption>
            <NativeSelectOption value='exclusive'>
              {t('Exclusive')}
            </NativeSelectOption>
          </NativeSelect>
        </label>
      </div>
      {props.lockedGroup && (
        <div className='bg-muted/40 flex flex-wrap items-center justify-between gap-2 rounded-lg border p-3 text-sm'>
          <p>
            {t(
              'Save or discard the current group draft before opening another group.'
            )}
          </p>
          <Button
            size='sm'
            variant='outline'
            onClick={(event) => {
              if (props.lockedGroup) {
                props.onOpen(props.lockedGroup, event.currentTarget)
              }
            }}
          >
            {t('Resume')}
          </Button>
        </div>
      )}
      {groups.length === 0 ? (
        <EmptyState title={t('No matching groups')} className='min-h-32' />
      ) : (
        <ul
          aria-label={t('All Groups')}
          className='divide-border/70 divide-y overflow-hidden rounded-xl border'
        >
          {groups.slice(0, limit).map((id) => {
            const entry = Object.hasOwn(props.groups, id)
              ? props.groups[id]
              : undefined
            const label = entry?.label?.trim() || id
            const platformName = catalogPlatforms.find(
              ([key]) => key === (entry?.platform ?? '')
            )?.[1]
            return (
              <li key={id}>
                <Button
                  variant='ghost'
                  aria-haspopup='dialog'
                  aria-label={t('Open group details: {{name}} ({{id}})', {
                    name: label,
                    id,
                  })}
                  className='grid h-auto min-h-12 w-full min-w-0 grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-2 rounded-none p-3 text-start whitespace-normal @min-[530px]:grid-cols-[minmax(0,1fr)_minmax(8rem,auto)_auto] [&&]:focus-visible:ring-inset'
                  disabled={!!props.lockedGroup && props.lockedGroup !== id}
                  onClick={(event) => props.onOpen(id, event.currentTarget)}
                >
                  <span className='col-start-1 row-start-1 flex min-w-0 items-start gap-2.5'>
                    <span className='bg-muted mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg border'>
                      <MarketGroupIcon meta={entry} />
                    </span>
                    <span className='min-w-0'>
                      <span className='block text-sm font-semibold [overflow-wrap:anywhere]'>
                        {label}
                      </span>
                      <span className='text-muted-foreground block font-mono text-xs [overflow-wrap:anywhere]'>
                        {id}
                      </span>
                    </span>
                  </span>
                  <span className='text-muted-foreground col-start-1 row-start-2 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs @min-[530px]:col-start-2 @min-[530px]:row-start-1'>
                    <span>
                      {platformName ? t(platformName) : entry?.platform}
                    </span>
                    <Badge variant={entry?.exclusive ? 'secondary' : 'outline'}>
                      {entry?.exclusive ? t('Exclusive') : t('Not exclusive')}
                    </Badge>
                    <span className='[overflow-wrap:anywhere] tabular-nums'>
                      {t('Base group rate')}: {props.baseRates[id]}×
                    </span>
                  </span>
                  <ChevronRight
                    aria-hidden='true'
                    className='text-muted-foreground col-start-2 row-start-1 size-4 @min-[530px]:col-start-3 rtl:rotate-180'
                  />
                </Button>
              </li>
            )
          })}
        </ul>
      )}
      {groups.length > limit && (
        <Button
          variant='outline'
          size='sm'
          onClick={() => setLimit(limit + pageSize)}
        >
          {t('Load more')}
        </Button>
      )}
    </div>
  )
}

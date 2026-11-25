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
import { Check, Search } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { GroupMultiplierBadge } from '@/components/group-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { GroupCatalogBadges } from '@/features/pricing/components/group-catalog-badges'
import { MarketGroupIcon } from '@/features/pricing/components/market-group-icon'
import {
  MARKET_PLATFORMS,
  type MarketPlatform,
} from '@/features/pricing/lib/market-groups'
import { cn } from '@/lib/utils'

export type KeyGroupCard = {
  id: string
  label: string
  description: string
  platform: MarketPlatform
  sortOrder: number
  ratio: number | string
  badges?: string[]
  exclusive?: boolean
  icon?: string
}

export function KeyGroupCards({
  groups,
  value,
  onChange,
  disabled,
}: {
  groups: KeyGroupCard[]
  value: string
  onChange: (id: string) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const [platform, setPlatform] = useState('')
  const [search, setSearch] = useState('')
  const [sort, setSort] = useState<'default' | 'name' | 'ratio'>('default')
  const availablePlatforms = new Set(groups.map((group) => group.platform))
  const platforms = (Object.keys(MARKET_PLATFORMS) as MarketPlatform[]).filter(
    (item) => availablePlatforms.has(item)
  )
  const selected = groups.find((group) => group.id === value)
  const filter = search.trim().toLocaleLowerCase()
  const matches = groups
    .filter(
      (group) =>
        (!platform || platform === group.platform) &&
        [group.id, group.label, group.description].some((part) =>
          part.toLocaleLowerCase().includes(filter)
        )
    )
    .sort((a, b) => {
      const aRatio = typeof a.ratio === 'number' ? a.ratio : Infinity
      const bRatio = typeof b.ratio === 'number' ? b.ratio : Infinity
      const byName = a.label.localeCompare(b.label) || a.id.localeCompare(b.id)
      if (sort === 'ratio' && aRatio !== bRatio) return aRatio - bRatio
      if (sort === 'default') return a.sortOrder - b.sortOrder || byName
      return byName
    })
  return (
    <fieldset className='min-w-0 space-y-2.5' disabled={disabled}>
      <legend className='pb-1 text-sm font-semibold'>
        {t('Primary group')}
      </legend>
      {selected && (
        <p
          role='status'
          className='text-primary flex min-w-0 items-start gap-1.5 text-xs font-medium'
        >
          <Check className='size-3.5 shrink-0' aria-hidden />
          <span className='min-w-0 [overflow-wrap:anywhere]'>
            {t('Selected: {{group}}', { group: selected.label })}
          </span>
        </p>
      )}
      <div
        role='group'
        className='flex flex-wrap gap-1.5'
        aria-label={t('Platform')}
      >
        <Button
          type='button'
          variant={platform ? 'outline' : 'default'}
          size='xs'
          aria-pressed={!platform}
          onClick={() => setPlatform('')}
        >
          {t('All')}
        </Button>
        {platforms.map((name) => (
          <Button
            key={name}
            type='button'
            variant={platform === name ? 'default' : 'outline'}
            size='xs'
            aria-pressed={platform === name}
            onClick={() => setPlatform(name)}
          >
            <span className='inline-flex shrink-0' aria-hidden='true'>
              <MarketGroupIcon platform={name} size={14} />
            </span>
            {name === 'other' ? t('Other') : MARKET_PLATFORMS[name]}
          </Button>
        ))}
      </div>
      <div className='flex flex-wrap items-center gap-2'>
        <div className='relative min-w-35 flex-1'>
          <Search
            className='text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-4 -translate-y-1/2'
            aria-hidden='true'
          />
          <Input
            className='pl-8'
            aria-label={t('Search group name')}
            placeholder={t('Search group name')}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
        </div>
        <NativeSelect
          className='max-w-full'
          size='sm'
          aria-label={t('Sort groups')}
          value={sort}
          onChange={(event) => {
            const next = event.target.value
            setSort(next === 'ratio' || next === 'name' ? next : 'default')
          }}
        >
          <NativeSelectOption value='default'>
            {t('Default')}
          </NativeSelectOption>
          <NativeSelectOption value='name'>{t('Name')}</NativeSelectOption>
          <NativeSelectOption value='ratio'>
            {t('Group multiplier')}
          </NativeSelectOption>
        </NativeSelect>
      </div>
      <div className='grid grid-cols-1 gap-2 sm:grid-cols-2'>
        {matches.map((group) => {
          const checked = group.id === value
          const cardId = `${id}-${encodeURIComponent(group.id)}`
          const nameId = `${cardId}-name`
          const detailId = `${cardId}-detail`
          const ratio =
            typeof group.ratio === 'number' && Number.isFinite(group.ratio)
              ? group.ratio
              : undefined
          return (
            <label
              key={group.id}
              className={cn(
                'focus-within:ring-ring hover:bg-muted/40 relative flex min-w-0 items-start gap-2 rounded-lg border px-3 py-2.5 text-sm focus-within:ring-2',
                checked && 'border-primary bg-primary/5 hover:bg-primary/10',
                disabled ? 'cursor-not-allowed opacity-60' : 'cursor-pointer'
              )}
            >
              {/* The whole card selects the same native radio; browser arrow/Space behavior stays intact. */}
              <input
                type='radio'
                name={id}
                value={group.id}
                checked={checked}
                onChange={() => onChange(group.id)}
                aria-labelledby={nameId}
                aria-describedby={detailId}
                className='accent-primary mt-1 size-3.5 shrink-0'
              />
              <span className='min-w-0 flex-1'>
                <span className='flex min-w-0 items-start gap-1.5'>
                  <span
                    className='mt-0.5 inline-flex shrink-0'
                    aria-hidden='true'
                  >
                    <MarketGroupIcon
                      meta={{ icon: group.icon }}
                      platform={group.platform}
                      size={15}
                    />
                  </span>
                  <span
                    id={nameId}
                    className='min-w-0 font-semibold [overflow-wrap:anywhere]'
                  >
                    {group.label}
                  </span>
                </span>
                <span
                  id={detailId}
                  className='mt-1 flex min-w-0 flex-wrap items-center gap-1.5 text-xs'
                >
                  {ratio !== undefined && (
                    <GroupMultiplierBadge
                      ratio={ratio}
                      label={`${ratio}×`}
                      className='text-xs'
                    />
                  )}{' '}
                  {group.exclusive && (
                    <Badge
                      variant='secondary'
                      className='h-auto rounded px-1.5 py-0.5'
                    >
                      {t('Exclusive')}
                    </Badge>
                  )}{' '}
                  <GroupCatalogBadges values={group.badges} />{' '}
                  {group.description && (
                    <span className='text-muted-foreground min-w-0 basis-full [overflow-wrap:anywhere]'>
                      {group.description}
                    </span>
                  )}
                </span>
              </span>
              {checked && (
                <Check className='text-primary size-4 shrink-0' aria-hidden />
              )}
            </label>
          )
        })}
        {matches.length === 0 && (
          <p className='text-muted-foreground col-span-full p-4 text-center text-sm'>
            {t('No group found.')}
          </p>
        )}
      </div>
    </fieldset>
  )
}

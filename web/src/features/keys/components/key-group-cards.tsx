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
import { ArrowLeft, Check, Search } from 'lucide-react'
import {
  useId,
  useRef,
  useState,
  type ComponentType,
  type MouseEvent,
} from 'react'
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
import { useMediaQuery } from '@/hooks/use-media-query'
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

export type KeyGroupPreviewDetails = ComponentType<{ groupId: string }>

type SortOrder = 'default' | 'ascending' | 'descending'

function numericRate(group: KeyGroupCard): number | undefined {
  return typeof group.ratio === 'number' && Number.isFinite(group.ratio)
    ? group.ratio
    : undefined
}

function compareGroups(
  a: KeyGroupCard,
  b: KeyGroupCard,
  sort: SortOrder
): number {
  const byName = a.label.localeCompare(b.label) || a.id.localeCompare(b.id)
  if (sort === 'default') return a.sortOrder - b.sortOrder || byName
  const aRatio = numericRate(a)
  const bRatio = numericRate(b)
  if (aRatio === undefined || bRatio === undefined) {
    return Number(aRatio === undefined) - Number(bRatio === undefined) || byName
  }
  return (sort === 'ascending' ? aRatio - bRatio : bRatio - aRatio) || byName
}

function GroupIdentity(props: { group: KeyGroupCard; nameId?: string }) {
  return (
    <span className='flex min-w-0 items-start gap-1.5'>
      <span className='mt-0.5 inline-flex shrink-0' aria-hidden='true'>
        <MarketGroupIcon
          meta={{ icon: props.group.icon }}
          platform={props.group.platform}
          size={16}
        />
      </span>
      <span
        id={props.nameId}
        className='min-w-0 font-semibold [overflow-wrap:anywhere]'
      >
        {props.group.label}
      </span>
    </span>
  )
}

function GroupBadges(props: { group: KeyGroupCard; id?: string }) {
  const { t } = useTranslation()
  if (!props.group.exclusive && !props.group.badges?.length) return null
  return (
    <span id={props.id} className='flex min-w-0 flex-wrap items-center gap-1.5'>
      {props.group.exclusive && (
        <Badge variant='secondary' className='h-auto rounded px-1.5 py-0.5'>
          {t('Exclusive')}
        </Badge>
      )}
      <GroupCatalogBadges values={props.group.badges} />
    </span>
  )
}

export function KeyGroupCards(props: {
  groups: KeyGroupCard[]
  value: string
  onChange: (id: string) => void
  disabled?: boolean
  previewDetails?: KeyGroupPreviewDetails
}) {
  const { t } = useTranslation()
  const id = useId()
  const [platform, setPlatform] = useState<MarketPlatform | ''>('')
  const [search, setSearch] = useState('')
  const [sort, setSort] = useState<SortOrder>('default')
  // Preview can read analytics but never selects a billing group or writes a Key.
  const [previewId, setPreviewId] = useState<string>()
  const isNarrow = useMediaQuery('(max-width: 1023px)')
  const inspectorTitle = useRef<HTMLHeadingElement>(null)
  const returnFocus = useRef<HTMLButtonElement>(null)
  const preview = props.groups.find((group) => group.id === previewId)
  const selected = props.groups.find((group) => group.id === props.value)
  const counts = new Map<MarketPlatform, number>()
  for (const group of props.groups) {
    counts.set(group.platform, (counts.get(group.platform) ?? 0) + 1)
  }
  const platforms = (Object.keys(MARKET_PLATFORMS) as MarketPlatform[]).filter(
    (item) => counts.has(item)
  )
  const platformName = (value: MarketPlatform): string =>
    value === 'other' ? t('Other') : MARKET_PLATFORMS[value]
  const filter = search.trim().toLocaleLowerCase()
  const matches = props.groups
    .filter(
      (group) =>
        (!platform || platform === group.platform) &&
        [
          group.id,
          group.label,
          group.description,
          group.platform,
          platformName(group.platform),
        ].some((part) => part.toLocaleLowerCase().includes(filter))
    )
    .sort((a, b) => compareGroups(a, b, sort))
  const PreviewDetails = props.previewDetails
  const ratio = preview ? numericRate(preview) : undefined

  function openPreview(
    event: MouseEvent<HTMLButtonElement>,
    group: KeyGroupCard
  ) {
    returnFocus.current = event.currentTarget
    setPreviewId(group.id)
    if (isNarrow) requestAnimationFrame(() => inspectorTitle.current?.focus())
  }

  function backToGroups() {
    setPreviewId(undefined)
    requestAnimationFrame(() => returnFocus.current?.focus())
  }

  return (
    <fieldset className='min-w-0' disabled={props.disabled}>
      <legend className='pb-2 text-sm font-semibold'>
        {t('Primary group')}
      </legend>
      {selected && (
        <p
          role='status'
          className='text-primary mb-2 flex min-w-0 items-start gap-1.5 text-xs font-medium'
        >
          <Check className='size-3.5 shrink-0' aria-hidden />
          <span className='min-w-0 [overflow-wrap:anywhere]'>
            {t('Selected: {{group}}', { group: selected.label })}
          </span>
        </p>
      )}

      <div className='grid min-w-0 gap-3 lg:grid-cols-[minmax(8.5rem,0.66fr)_minmax(0,1.55fr)_minmax(0,1.2fr)]'>
        <div
          role='group'
          aria-label={t('Platform')}
          className={cn(
            'flex min-w-0 flex-wrap items-start gap-1.5 lg:flex-col lg:flex-nowrap lg:rounded-lg lg:border lg:p-2',
            preview && 'hidden lg:flex'
          )}
        >
          <h3 className='text-muted-foreground hidden px-2 text-xs font-medium lg:block'>
            {t('Platform')}
          </h3>
          <Button
            type='button'
            variant={platform ? 'ghost' : 'secondary'}
            size='sm'
            className='lg:w-full lg:justify-between'
            aria-pressed={!platform}
            aria-label={`${t('All')} ${props.groups.length}`}
            onClick={() => setPlatform('')}
          >
            {t('All')}{' '}
            <span className='text-muted-foreground tabular-nums'>
              {props.groups.length}
            </span>
          </Button>
          {platforms.map((item) => (
            <Button
              key={item}
              type='button'
              variant={platform === item ? 'secondary' : 'ghost'}
              size='sm'
              className='lg:w-full lg:justify-between'
              aria-pressed={platform === item}
              aria-label={`${platformName(item)} ${counts.get(item)}`}
              onClick={() => setPlatform(item)}
            >
              <span className='inline-flex min-w-0 items-center gap-1.5'>
                <span className='inline-flex shrink-0' aria-hidden='true'>
                  <MarketGroupIcon platform={item} size={14} />
                </span>
                {platformName(item)}
              </span>
              <span className='text-muted-foreground tabular-nums'>
                {counts.get(item)}
              </span>
            </Button>
          ))}
        </div>

        <div
          className={cn(
            'min-w-0 space-y-2 rounded-lg border p-2.5',
            preview && 'hidden lg:block'
          )}
        >
          <div className='flex flex-wrap items-center gap-2'>
            <div className='relative min-w-36 flex-1'>
              <Search
                className='text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-4 -translate-y-1/2'
                aria-hidden
              />
              <Input
                className='pl-8'
                aria-label={t('Search by group name, description, or platform')}
                placeholder={t(
                  'Search by group name, description, or platform'
                )}
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') event.preventDefault()
                }}
              />
            </div>
            <NativeSelect
              className='max-w-full'
              size='sm'
              aria-label={t('Sort groups')}
              value={sort}
              onChange={(event) => {
                const value = event.target.value
                setSort(
                  value === 'ascending' || value === 'descending'
                    ? value
                    : 'default'
                )
              }}
            >
              <NativeSelectOption value='default'>
                {t('Default')}
              </NativeSelectOption>
              <NativeSelectOption value='ascending'>
                {t('Multiplier: Low to High')}
              </NativeSelectOption>
              <NativeSelectOption value='descending'>
                {t('Multiplier: High to Low')}
              </NativeSelectOption>
            </NativeSelect>
          </div>
          <div
            role='group'
            aria-label={t('Groups')}
            className='grid min-w-0 gap-1.5 lg:max-h-[clamp(14rem,44dvh,32rem)] lg:overflow-y-auto lg:overscroll-contain lg:p-1'
          >
            {matches.map((group) => {
              const cardId = `${id}-${encodeURIComponent(group.id)}`
              const rate = numericRate(group)
              const descriptions = [`${cardId}-desc`]
              if (rate !== undefined) descriptions.push(`${cardId}-rate`)
              if (group.exclusive || group.badges?.length) {
                descriptions.push(`${cardId}-badges`)
              }
              return (
                <Button
                  key={group.id}
                  type='button'
                  variant='outline'
                  aria-labelledby={`${cardId}-name`}
                  aria-describedby={descriptions.join(' ')}
                  aria-pressed={preview?.id === group.id}
                  className={cn(
                    'h-auto min-h-11 w-full min-w-0 justify-between gap-2 rounded-md px-2 py-2 text-start whitespace-normal',
                    preview?.id === group.id && 'border-primary bg-primary/5'
                  )}
                  onClick={(event) => openPreview(event, group)}
                >
                  <span className='min-w-0 space-y-1'>
                    <GroupIdentity group={group} nameId={`${cardId}-name`} />
                    <span
                      id={`${cardId}-desc`}
                      className='text-muted-foreground block min-w-0 text-xs font-normal [overflow-wrap:anywhere]'
                    >
                      {group.description}
                    </span>
                    <GroupBadges group={group} id={`${cardId}-badges`} />
                  </span>
                  <span className='flex shrink-0 items-center gap-1'>
                    {rate !== undefined && (
                      <span id={`${cardId}-rate`}>
                        <GroupMultiplierBadge
                          ratio={rate}
                          label={`${rate}×`}
                          className='text-xs'
                        />
                      </span>
                    )}
                    {group.id === props.value && (
                      <Check
                        className='text-primary size-4 shrink-0'
                        aria-hidden
                      />
                    )}
                  </span>
                </Button>
              )
            })}
            {matches.length === 0 && (
              <p className='text-muted-foreground p-3 text-center text-sm'>
                {t('No group found.')}
              </p>
            )}
          </div>
        </div>

        <section
          aria-labelledby={`${id}-preview-heading`}
          className={cn(
            'min-w-0 space-y-3 rounded-lg border p-3',
            !preview && 'hidden lg:block'
          )}
        >
          {!!preview && (
            <Button
              type='button'
              size='sm'
              variant='ghost'
              className='px-0 lg:hidden'
              onClick={backToGroups}
            >
              <ArrowLeft aria-hidden className='size-4' />
              {t('Back to group list')}
            </Button>
          )}
          <h3
            id={`${id}-preview-heading`}
            ref={inspectorTitle}
            tabIndex={-1}
            className='text-sm font-semibold'
          >
            {t('Group preview')}
          </h3>
          {preview ? (
            <>
              <GroupIdentity group={preview} />
              <GroupBadges group={preview} />
              {preview.description && (
                <p className='text-muted-foreground text-xs [overflow-wrap:anywhere]'>
                  {preview.description}
                </p>
              )}
              {ratio !== undefined && (
                <div className='flex flex-wrap items-center gap-2 text-xs'>
                  {t('Effective rate')}
                  <GroupMultiplierBadge ratio={ratio} label={`${ratio}×`} />
                </div>
              )}
              {PreviewDetails && <PreviewDetails groupId={preview.id} />}
              <Button
                type='button'
                className='h-auto min-h-8 w-full whitespace-normal'
                disabled={preview.id === props.value}
                onClick={() => props.onChange(preview.id)}
              >
                {t('Set as primary channel')}
              </Button>
            </>
          ) : (
            <p className='text-muted-foreground text-xs'>
              {t('Select a group to preview its details.')}
            </p>
          )}
        </section>
      </div>
    </fieldset>
  )
}

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
import { ArrowUpDown, Check, RotateCcw } from 'lucide-react'
import { Fragment } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTableViewModeToggle } from '@/components/data-table'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'

import {
  canSortCatalogBy,
  getSortLabels,
  MODEL_ORDER_SORTS,
  type SortOption,
  type ViewMode,
} from '../constants'
import type { ModelOrder, TokenUnit } from '../types'

export interface PricingToolbarProps {
  filteredCount: number
  totalCount?: number
  platformBest?: boolean
  hasActiveFilters?: boolean
  onClearFilters?: () => void
  sortBy: string
  modelOrder?: ModelOrder
  onSortChange: (value: string) => void
  tokenUnit: TokenUnit
  onTokenUnitChange: (value: TokenUnit) => void
  showRechargePrice: boolean
  onRechargePriceChange: (value: boolean) => void
  viewMode: ViewMode
  onViewModeChange: (value: ViewMode) => void
}

export function PricingToolbar(props: PricingToolbarProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const sortLabels = getSortLabels(t)
  const sortEntries = Object.entries(sortLabels).filter(([value]) =>
    canSortCatalogBy(value, props.modelOrder)
  )
  const sortGroups = [
    {
      id: 'order',
      entries: sortEntries.filter(([value]) => MODEL_ORDER_SORTS.has(value)),
    },
    {
      id: 'other',
      entries: sortEntries.filter(([value]) => !MODEL_ORDER_SORTS.has(value)),
    },
  ].filter((group) => group.entries.length > 0)

  return (
    <div className='min-w-0 border-b pb-3'>
      <div className='flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-3'>
        <div className='flex min-w-0 flex-wrap items-center gap-2'>
          <p
            role='status'
            className='text-muted-foreground flex flex-wrap items-baseline gap-x-1.5 text-sm'
          >
            {t('Matching models')}
            <strong className='text-foreground font-semibold tabular-nums'>
              {formatNumber(props.filteredCount, locale)}
            </strong>
            {props.totalCount != null &&
              props.filteredCount !== props.totalCount && (
                <span className='tabular-nums'>
                  / {formatNumber(props.totalCount, locale)}
                </span>
              )}
          </p>
          {props.platformBest && (
            <span className='text-muted-foreground text-xs'>
              {t('Lowest price on this platform')}
            </span>
          )}
          {props.hasActiveFilters && props.onClearFilters && (
            <Button
              type='button'
              variant='ghost'
              size='sm'
              onClick={props.onClearFilters}
              aria-label={t('Reset filters')}
            >
              <RotateCcw aria-hidden className='size-3.5' />
              {t('Reset')}
            </Button>
          )}
        </div>

        <div className='flex min-w-0 flex-wrap items-center gap-2'>
          <ToggleGroup
            value={[props.showRechargePrice ? 'recharge' : 'standard']}
            onValueChange={(values) => {
              if (values.length > 0) {
                props.onRechargePriceChange(values[0] === 'recharge')
              }
            }}
            variant='outline'
            size='sm'
            aria-label={t('Price display mode')}
          >
            <ToggleGroupItem value='standard'>{t('Standard')}</ToggleGroupItem>
            <ToggleGroupItem value='recharge'>{t('Recharge')}</ToggleGroupItem>
          </ToggleGroup>
          <ToggleGroup
            value={[props.tokenUnit]}
            onValueChange={(values) => {
              if (values[0] === 'M' || values[0] === 'K') {
                props.onTokenUnitChange(values[0])
              }
            }}
            variant='outline'
            size='sm'
            aria-label={t('Token unit')}
          >
            <ToggleGroupItem value='M'>/1M</ToggleGroupItem>
            <ToggleGroupItem value='K'>/1K</ToggleGroupItem>
          </ToggleGroup>

          <DropdownMenu modal={false}>
            <DropdownMenuTrigger
              render={
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  className='h-auto min-h-8 max-w-full gap-1.5 px-3 py-1 text-xs'
                />
              }
            >
              <ArrowUpDown aria-hidden className='size-3.5' />
              <span className='min-w-0 [overflow-wrap:anywhere] whitespace-normal'>
                {sortLabels[props.sortBy as SortOption] || t('Sort')}
              </span>
            </DropdownMenuTrigger>
            <DropdownMenuContent align='end' className='w-44'>
              {sortGroups.map((group, index) => (
                <Fragment key={group.id}>
                  {index > 0 && <DropdownMenuSeparator />}
                  <DropdownMenuGroup>
                    {group.entries.map(([value, label]) => (
                      <DropdownMenuItem
                        key={value}
                        onClick={() => props.onSortChange(value)}
                        className='gap-2 [overflow-wrap:anywhere] whitespace-normal'
                      >
                        <Check
                          className={cn(
                            'size-4 shrink-0',
                            props.sortBy === value ? 'opacity-100' : 'opacity-0'
                          )}
                        />
                        {label}
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuGroup>
                </Fragment>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>

          <DataTableViewModeToggle
            value={props.viewMode}
            onChange={props.onViewModeChange}
          />
        </div>
      </div>
    </div>
  )
}

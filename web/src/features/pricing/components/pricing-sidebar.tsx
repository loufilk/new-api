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
import { RotateCcw } from 'lucide-react'
import { memo, useMemo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'

import {
  FILTER_ALL,
  getEndpointTypeLabels,
  getQuotaTypeLabels,
} from '../constants'
import { getModelQuotaType } from '../lib/filters'
import { groupCatalogBadgeLabels } from '../lib/group-catalog-badges'
import {
  groupPlatform,
  marketGroupLabel,
  MARKET_PLATFORMS,
  type MarketPlatform,
} from '../lib/market-groups'
import type { PricingModel, MarketGroupCatalog } from '../types'
import { GroupCatalogBadges } from './group-catalog-badges'
import { SearchBar, type SearchBarProps } from './search-bar'

interface Option {
  value: string
  label: string
  suffix?: string | number
  description?: string
  leading?: ReactNode
  accessiblePrefix?: string[]
}

const PLATFORM_ICONS: Partial<Record<MarketPlatform, string>> = {
  openai: 'OpenAI',
  anthropic: 'Claude.Color',
  gemini: 'Gemini.Color',
  deepseek: 'DeepSeek.Color',
  moonshot: 'Moonshot',
  xai: 'XAI',
  zhipu: 'Zhipu.Color',
  azure: 'Azure.Color',
}

function PlatformIcon(props: { platform: MarketPlatform }) {
  const key = PLATFORM_ICONS[props.platform]
  return key ? getLobeIcon(key, 15) : null
}

function FilterRow(props: {
  title: string
  value: string
  options: Option[]
  onChange: (value: string) => void
}) {
  return (
    <div
      role='group'
      aria-label={props.title}
      className='grid min-w-0 gap-2 sm:grid-cols-[5.25rem_minmax(0,1fr)]'
    >
      <span className='text-muted-foreground self-start pt-1 text-sm font-medium'>
        {props.title}
      </span>
      <div className='flex min-w-0 flex-wrap gap-x-2 gap-y-2'>
        {props.options.map((option) => {
          const active = props.value === option.value
          return (
            <Button
              key={option.value}
              variant={active ? 'default' : 'outline'}
              size='sm'
              className='h-auto min-h-8 max-w-full gap-1.5 rounded-full px-3 py-1 text-sm'
              type='button'
              aria-pressed={active}
              aria-label={[
                ...(option.accessiblePrefix ?? []),
                option.label,
                option.suffix,
              ]
                .filter((part) => part != null)
                .join(' ')}
              onClick={() => props.onChange(option.value)}
              title={option.description || option.label}
            >
              {option.leading}
              <span className='break-all whitespace-normal'>
                {option.label}
              </span>
              {option.suffix != null && (
                <span
                  className={cn(
                    'shrink-0 rounded-full px-1.5 text-xs tabular-nums',
                    active
                      ? 'bg-background/20'
                      : 'bg-muted text-muted-foreground'
                  )}
                >
                  {option.suffix}
                </span>
              )}
            </Button>
          )
        })}
      </div>
    </div>
  )
}

export interface PricingSidebarProps {
  platform: string
  quotaTypeFilter: string
  endpointTypeFilter: string
  groupFilter: string
  onPlatformChange: (value: string) => void
  onQuotaTypeChange: (value: string) => void
  onEndpointTypeChange: (value: string) => void
  onGroupChange: (value: string) => void
  groups: string[]
  groupCatalog?: MarketGroupCatalog
  groupRatios?: Record<string, number>
  models: PricingModel[]
  hasActiveFilters: boolean
  onClearFilters: () => void
  search: SearchBarProps
}

export const PricingSidebar = memo(function PricingSidebar(
  props: PricingSidebarProps
) {
  const { t } = useTranslation()
  const counts = useMemo(() => {
    const endpoints = new Map<string, number>()
    const quotas = { token: 0, request: 0, task: 0 }
    for (const model of props.models) {
      for (const type of new Set(model.supported_endpoint_types ?? [])) {
        endpoints.set(type, (endpoints.get(type) ?? 0) + 1)
      }
      const quota = getModelQuotaType(model)
      if (quota) quotas[quota]++
    }
    return { endpoints, quotas }
  }, [props.models])
  const platformCounts = useMemo(() => {
    const counts = new Map<MarketPlatform, number>()
    for (const group of props.groups) {
      const key = groupPlatform(props.groupCatalog?.[group])
      counts.set(key, (counts.get(key) ?? 0) + 1)
    }
    return counts
  }, [props.groups, props.groupCatalog])
  const all = (suffix?: number): Option => ({
    value: FILTER_ALL,
    label: t('All'),
    suffix,
  })
  const platformOptions = [
    all(props.groups.length),
    ...Object.entries(MARKET_PLATFORMS).flatMap(([key, label]) => {
      const value = key as MarketPlatform
      const count = platformCounts.get(value)
      return count
        ? [
            {
              value,
              label: value === 'other' ? t('Other') : label,
              leading: <PlatformIcon platform={value} />,
              suffix: count,
            },
          ]
        : []
    }),
  ]
  const groupOptions = [
    all(),
    ...props.groups
      .filter(
        (group) =>
          props.platform === FILTER_ALL ||
          props.platform === groupPlatform(props.groupCatalog?.[group])
      )
      .sort(
        (a, b) =>
          (props.groupCatalog?.[a]?.sort_order ?? 0) -
            (props.groupCatalog?.[b]?.sort_order ?? 0) ||
          marketGroupLabel(a, props.groupCatalog).localeCompare(
            marketGroupLabel(b, props.groupCatalog)
          )
      )
      .map((group): Option => {
        const meta = props.groupCatalog?.[group]
        const ratio = props.groupRatios?.[group]
        return {
          value: group,
          label: marketGroupLabel(group, props.groupCatalog),
          description: meta?.description,
          accessiblePrefix: [
            ...(meta?.exclusive ? [t('Exclusive')] : []),
            ...groupCatalogBadgeLabels(meta?.badges).map((label) => t(label)),
          ],
          suffix: ratio == null ? undefined : `×${ratio}`,
          leading: (
            <>
              <PlatformIcon platform={groupPlatform(meta)} />
              {meta?.exclusive && (
                <span className='rounded bg-violet-700 px-1 text-xs text-white'>
                  {t('Exclusive')}
                </span>
              )}
              <GroupCatalogBadges values={meta?.badges} />
            </>
          ),
        }
      }),
  ]
  const quotaLabels = getQuotaTypeLabels(t)
  const quotaOptions = [
    all(props.models.length),
    ...Object.entries(counts.quotas).map(([key, count]) => ({
      value: key,
      label: quotaLabels[key as keyof typeof counts.quotas],
      suffix: count,
    })),
  ]
  const endpointLabels: Record<string, string> = getEndpointTypeLabels(t)
  const endpointOptions = [
    all(props.models.length),
    ...[
      ...new Set([...Object.keys(endpointLabels), ...counts.endpoints.keys()]),
    ]
      .filter((key) => key !== FILTER_ALL)
      .map((key) => ({
        value: key,
        label: endpointLabels[key] ?? key,
        suffix: counts.endpoints.get(key) ?? 0,
      })),
  ]
  return (
    <section
      aria-label={t('Filter')}
      className='bg-card rounded-2xl border px-4 py-4 shadow-sm sm:px-5'
    >
      <div className='space-y-3'>
        <FilterRow
          title={t('Platform')}
          value={props.platform}
          options={platformOptions}
          onChange={props.onPlatformChange}
        />
        <FilterRow
          title={t('Group')}
          value={props.groupFilter}
          options={groupOptions}
          onChange={props.onGroupChange}
        />
        <FilterRow
          title={t('Pricing Type')}
          value={props.quotaTypeFilter}
          options={quotaOptions}
          onChange={props.onQuotaTypeChange}
        />
        <FilterRow
          title={t('Endpoint Type')}
          value={props.endpointTypeFilter}
          options={endpointOptions}
          onChange={props.onEndpointTypeChange}
        />
      </div>
      <div className='mt-4 flex items-center gap-2 border-t pt-3'>
        <SearchBar {...props.search} className='min-w-0 flex-1' />
        <Button
          variant='ghost'
          size='sm'
          onClick={props.onClearFilters}
          disabled={!props.hasActiveFilters && !props.search.value}
        >
          <RotateCcw aria-hidden className='size-3.5' />
          {t('Reset')}
        </Button>
      </div>
    </section>
  )
})

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
import { ChevronDown } from 'lucide-react'
import { memo, useId, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { useMediaQuery } from '@/hooks/use-media-query'
import { cn } from '@/lib/utils'

import {
  FILTER_ALL,
  MAX_FILTER_ITEMS,
  getEndpointTypeLabels,
  getQuotaTypeLabels,
} from '../constants'
import { getModelQuotaType } from '../lib/filters'
import { groupCatalogBadgeLabels } from '../lib/group-catalog-badges'
import {
  getRecentGroupActivity,
  type RecentGroupActivity,
} from '../lib/group-market-activity'
import {
  groupPlatform,
  marketGroupLabel,
  MARKET_PLATFORMS,
  type MarketPlatform,
} from '../lib/market-groups'
import { getModelEndpointTypes } from '../lib/model-endpoints'
import type {
  PricingModel,
  MarketActivityByGroup,
  MarketGroupCatalog,
} from '../types'
import { GroupCatalogBadges } from './group-catalog-badges'
import { GroupMarketBadges } from './group-market-activity'
import { MarketGroupIcon } from './market-group-icon'

interface Option {
  value: string
  label: string
  suffix?: string | number
  description?: string
  leading?: ReactNode
  activity?: RecentGroupActivity
  accessiblePrefix?: string[]
}

function FilterRow(props: {
  title: string
  description?: string
  collapsible?: boolean
  value: string
  options: Option[]
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const [expanded, setExpanded] = useState(false)
  const compact = Boolean(
    props.collapsible && props.options.length > MAX_FILTER_ITEMS
  )
  const visible =
    compact && !expanded
      ? props.options.filter(
          (option, index) =>
            index < MAX_FILTER_ITEMS || option.value === props.value
        )
      : props.options
  return (
    <div
      role='group'
      aria-describedby={props.description ? `${id}-description` : undefined}
      aria-label={props.title}
      className='grid min-w-0 gap-2 py-3 first:pt-0 last:pb-0 sm:grid-cols-[6.5rem_minmax(0,1fr)] sm:gap-x-4'
    >
      <div className='flex flex-wrap items-baseline gap-x-2 sm:block'>
        <span className='text-foreground text-sm font-semibold'>
          {props.title}
        </span>
        {props.description && (
          <span
            id={`${id}-description`}
            className='text-muted-foreground text-xs leading-snug sm:mt-0.5 sm:block'
          >
            {props.description}
          </span>
        )}
      </div>
      <div id={id} className='flex min-w-0 flex-wrap items-center gap-1.5'>
        {visible.map((option, index) => {
          const active = props.value === option.value
          return (
            <Button
              key={option.value}
              variant={active ? 'default' : 'outline'}
              size='sm'
              className='h-auto min-h-8 max-w-full min-w-0 flex-wrap gap-x-1.5 gap-y-1 rounded-full px-3 py-1 text-sm'
              type='button'
              aria-pressed={active}
              aria-describedby={
                option.activity ? `${id}-activity-${index}` : undefined
              }
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
              <span className='min-w-0 text-start [overflow-wrap:anywhere] whitespace-normal'>
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
              {option.activity && (
                <GroupMarketBadges
                  id={`${id}-activity-${index}`}
                  activity={option.activity}
                />
              )}
            </Button>
          )
        })}
        {compact && (
          <Button
            type='button'
            variant='ghost'
            size='sm'
            aria-controls={id}
            aria-expanded={expanded}
            onClick={() => setExpanded((open) => !open)}
          >
            {expanded ? t('Collapse') : t('Expand')}
            <ChevronDown
              aria-hidden
              className={cn('size-3.5', expanded && 'rotate-180')}
            />
          </Button>
        )}
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
  groupMarketActivity?: MarketActivityByGroup
  groupRatios?: Record<string, number>
  models: PricingModel[]
}

export const PricingSidebar = memo(function PricingSidebar(
  props: PricingSidebarProps
) {
  const { t } = useTranslation()
  const compact = useMediaQuery('(max-width: 639px)')
  const counts = useMemo(() => {
    const endpoints = new Map<string, number>()
    const quotas = { token: 0, request: 0, task: 0 }
    const groupModels =
      props.groupFilter === FILTER_ALL
        ? props.models
        : props.models.filter((model) =>
            model.enable_groups.includes(props.groupFilter)
          )
    for (const model of groupModels) {
      for (const type of new Set(
        getModelEndpointTypes(model, props.groupFilter)
      )) {
        endpoints.set(type, (endpoints.get(type) ?? 0) + 1)
      }
      const quota = getModelQuotaType(model)
      if (quota) quotas[quota]++
    }
    return { endpoints, quotas, total: groupModels.length }
  }, [props.models, props.groupFilter])
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
              leading: <MarketGroupIcon platform={value} />,
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
          activity: getRecentGroupActivity(props.groupMarketActivity?.[group]),
          accessiblePrefix: [
            ...(meta?.exclusive ? [t('Exclusive')] : []),
            ...groupCatalogBadgeLabels(meta?.badges).map((label) => t(label)),
          ],
          suffix: ratio == null ? undefined : `×${ratio}`,
          leading: (
            <>
              <MarketGroupIcon meta={meta} />
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
    all(counts.total),
    ...Object.entries(counts.quotas).map(([key, count]) => ({
      value: key,
      label: quotaLabels[key as keyof typeof counts.quotas],
      suffix: count,
    })),
  ]
  const endpointLabels: Record<string, string> = getEndpointTypeLabels(t)
  const endpointOptions = [
    all(counts.total),
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
      className='bg-card min-w-0 rounded-2xl border px-4 py-4 shadow-sm sm:px-6 sm:py-5'
    >
      <div className='divide-border/60 divide-y'>
        <FilterRow
          title={t('Platform')}
          description={t('Available group counts')}
          value={props.platform}
          options={platformOptions}
          onChange={props.onPlatformChange}
        />
        <FilterRow
          key={props.platform}
          title={t('Group')}
          collapsible={compact}
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
    </section>
  )
})

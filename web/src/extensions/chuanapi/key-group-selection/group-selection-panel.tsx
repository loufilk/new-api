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
import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import {
  Command,
  CommandGroup,
  CommandInput,
  CommandList,
} from '@/components/ui/command'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import type { ApiKeyGroupOption } from '@/features/keys/components/api-key-group-combobox'
import { ApiKeyGroupHealth } from '@/features/keys/components/api-key-group-health'
import { AutoGroupOrderEditor } from '@/features/keys/components/auto-group-order-editor'
import { GroupRatioBadge } from '@/features/keys/components/auto-group-visuals'

import { GroupCatalogCard } from './group-catalog-card'

type GroupSelectionPanelProps = {
  options: ApiKeyGroupOption[]
  values: string[]
  maxCount: number
  disabled?: boolean
  onChange: (values: string[]) => void
  onSelect: (value: string) => void
}

export function GroupSelectionPanel(props: GroupSelectionPanelProps) {
  const { t } = useTranslation()
  const id = useId()
  const [search, setSearch] = useState('')
  const [filter, setFilter] = useState('all')
  const filteredOptions = useMemo(() => {
    const query = search.trim().toLowerCase()
    return props.options.filter((option) => {
      if (filter === 'selected' && !props.values.includes(option.value)) {
        return false
      }
      return [
        option.value,
        option.label,
        option.desc ?? '',
        String(option.ratio ?? ''),
      ]
        .join(' ')
        .toLowerCase()
        .includes(query)
    })
  }, [props.options, props.values, search, filter])
  const primaryGroup = props.values[0]
  const primaryOption = props.options.find(
    (option) => option.value === primaryGroup
  )

  return (
    <Command
      shouldFilter={false}
      label={t('Search group name')}
      className='grid min-w-0 gap-4 overflow-visible p-0 lg:grid-cols-[10rem_minmax(0,1fr)_20rem]'
    >
      <section
        aria-labelledby={`${id}-filters`}
        className='flex min-w-0 flex-col gap-3 rounded-xl border p-3'
      >
        <h3 id={`${id}-filters`} className='text-sm font-medium'>
          {t('Group filters')}
        </h3>
        <CommandInput
          aria-label={t('Search group name')}
          placeholder={t('Search group name')}
          value={search}
          onValueChange={setSearch}
          disabled={props.disabled}
        />
        <ToggleGroup
          orientation='vertical'
          aria-label={t('Group filters')}
          value={[filter]}
          onValueChange={(values) => setFilter(values[0] ?? 'all')}
          disabled={props.disabled}
          spacing={1}
          className='w-full'
        >
          <ToggleGroupItem value='all' className='justify-start'>
            {t('All groups')}
          </ToggleGroupItem>
          <ToggleGroupItem value='selected' className='justify-start'>
            {t('Selected groups')}
          </ToggleGroupItem>
        </ToggleGroup>
        <p className='text-muted-foreground text-xs leading-relaxed'>
          {t(
            'Group multipliers shown here are the effective values for your account.'
          )}
        </p>
      </section>
      <section
        aria-labelledby={`${id}-catalog`}
        className='min-w-0 rounded-xl border p-3'
      >
        <h3 id={`${id}-catalog`} className='mb-3 text-sm font-medium'>
          {t('Group catalog')}
        </h3>
        <CommandList
          label={t('Available groups')}
          aria-multiselectable
          className='max-h-[40dvh] min-w-0 overscroll-contain lg:max-h-[30rem]'
        >
          {filteredOptions.length === 0 ? (
            <EmptyState title={t('No group found.')} className='min-h-36' />
          ) : (
            <CommandGroup className='p-0 [&_[cmdk-group-items]]:grid [&_[cmdk-group-items]]:gap-2'>
              {filteredOptions.map((option) => (
                <GroupCatalogCard
                  key={option.value}
                  option={option}
                  selected={props.values.includes(option.value)}
                  disabled={
                    props.disabled ||
                    (props.maxCount > 1 &&
                      !props.values.includes(option.value) &&
                      props.values.length >= props.maxCount)
                  }
                  onSelect={() => props.onSelect(option.value)}
                />
              ))}
            </CommandGroup>
          )}
        </CommandList>
      </section>
      <section
        aria-labelledby={`${id}-queue`}
        className='bg-muted/20 flex min-w-0 flex-col gap-3 rounded-xl border p-3'
      >
        <h3 id={`${id}-queue`} className='text-sm font-medium'>
          {t('Routing queue')}
        </h3>
        <p className='text-muted-foreground text-xs' aria-live='polite'>
          {t('{{count}} / {{max}} groups selected', {
            count: props.values.length,
            max: props.maxCount,
          })}
        </p>
        {primaryGroup ? (
          <div className='bg-background min-w-0 rounded-lg border p-3'>
            <p className='text-muted-foreground text-xs'>
              {t('Primary group')}
            </p>
            <div className='mt-2 flex min-w-0 flex-wrap items-center justify-between gap-2'>
              <p className='min-w-0 text-sm font-medium [overflow-wrap:anywhere]'>
                {primaryOption?.label ?? primaryGroup}
              </p>
              <GroupRatioBadge ratio={primaryOption?.ratio} />
            </div>
            {!primaryOption && (
              <p className='text-destructive mt-1 text-xs'>
                {t('Unavailable')}
              </p>
            )}
            <ApiKeyGroupHealth detail={primaryOption?.health} />
          </div>
        ) : (
          <EmptyState title={t('Select a group')} className='min-h-24' />
        )}
        {props.values.length > 0 && (
          <fieldset
            disabled={props.disabled}
            className='m-0 min-w-0 border-0 p-0'
          >
            <AutoGroupOrderEditor
              selectionOnly
              showGroupRatios
              aria-label={t('Routing queue')}
              value={props.values}
              mode='custom'
              options={props.options}
              globalOptions={[]}
              maxCount={props.maxCount}
              onChange={({ groups }) => {
                if (!props.disabled) props.onChange(groups)
              }}
              className='max-h-[40dvh] min-w-0 overflow-y-auto overscroll-contain lg:max-h-[22rem]'
            />
          </fieldset>
        )}
      </section>
    </Command>
  )
}

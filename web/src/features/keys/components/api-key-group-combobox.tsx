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
import { Check, ChevronsUpDown } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { useMediaQuery } from '@/hooks'
import { cn } from '@/lib/utils'

import type { TokenGroupDetail } from '../types'
import { ApiKeyGroupHealth } from './api-key-group-health'
import {
  AUTO_GROUP_FRAME_CLASS_NAME,
  AutoGroupFlowBorder,
  GroupRatioBadge,
} from './auto-group-visuals'

export type ApiKeyGroupOption = {
  value: string
  label: string
  desc?: string
  ratio?: number | string
  health?: TokenGroupDetail
}

type ApiKeyGroupComboboxProps = {
  options: ApiKeyGroupOption[]
  value?: string
  onValueChange: (value: string) => void
  placeholder?: string
  disabled?: boolean
  values?: string[]
  onValuesChange?: (values: string[]) => void
  maxCount?: number
  id?: string
  'aria-label'?: string
  'aria-describedby'?: string
  'aria-invalid'?: boolean | 'true' | 'false'
  'aria-required'?: boolean
}

export function ApiKeyGroupCombobox({
  options,
  value,
  onValueChange,
  placeholder,
  disabled,
  values,
  onValuesChange,
  maxCount = 5,
  id,
  'aria-label': ariaLabel,
  'aria-describedby': ariaDescribedBy,
  'aria-invalid': ariaInvalid,
  'aria-required': ariaRequired,
}: ApiKeyGroupComboboxProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [searchValue, setSearchValue] = useState('')
  const shouldReduceMotion = useMediaQuery('(prefers-reduced-motion: reduce)')
  const selectedOption = options.find((option) => option.value === value)
  const multiple = values !== undefined && onValuesChange !== undefined
  const isAutoSelected = !multiple && selectedOption?.value === 'auto'
  useEffect(() => {
    if (disabled) {
      setOpen(false)
      setSearchValue('')
    }
  }, [disabled])

  const filteredOptions = useMemo(() => {
    const search = searchValue.trim().toLowerCase()
    if (!search) return options

    return options.filter((option) => {
      const ratioText = String(option.ratio ?? '').toLowerCase()
      return (
        option.value.toLowerCase().includes(search) ||
        option.label.toLowerCase().includes(search) ||
        option.desc?.toLowerCase().includes(search) ||
        ratioText.includes(search)
      )
    })
  }, [options, searchValue])

  const handleSelect = (selectedValue: string) => {
    if (disabled) return
    if (multiple) {
      if (values.includes(selectedValue)) {
        onValuesChange(values.filter((item) => item !== selectedValue))
      } else if (maxCount === 1) {
        onValuesChange([selectedValue])
      } else if (values.length < maxCount) {
        onValuesChange([...values, selectedValue])
      }
      return
    }
    onValueChange(selectedValue)
    setOpen(false)
    setSearchValue('')
  }

  return (
    <Popover open={open && !disabled} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <Button
            type='button'
            variant='outline'
            id={id}
            role='combobox'
            aria-label={ariaLabel}
            aria-describedby={ariaDescribedBy}
            aria-invalid={ariaInvalid}
            aria-required={ariaRequired}
            aria-expanded={open}
            data-auto-group-effect={isAutoSelected ? 'trigger' : undefined}
            disabled={disabled}
            className={cn(
              'border-input bg-muted/40 hover:bg-muted/55 hover:text-foreground active:bg-background data-popup-open:border-ring data-popup-open:bg-background data-popup-open:ring-ring/20 relative h-auto min-h-14 w-full justify-between gap-2 rounded-lg px-3 py-2 text-start shadow-none transition-[background-color,border-color,box-shadow] duration-150 data-popup-open:ring-[3px] sm:min-h-20 sm:gap-3 sm:px-4 sm:py-3',
              multiple &&
                'min-h-8! rounded-md px-3! py-1.5! text-xs sm:min-h-8!',
              isAutoSelected &&
                cn(
                  AUTO_GROUP_FRAME_CLASS_NAME,
                  'hover:border-primary/55 data-popup-open:border-primary/55 data-popup-open:ring-primary/20'
                )
            )}
          />
        }
      >
        {isAutoSelected && (
          <AutoGroupFlowBorder shouldReduceMotion={shouldReduceMotion} />
        )}
        <span className='flex min-w-0 flex-1 items-center justify-between gap-2 sm:gap-3'>
          <span className='min-w-0'>
            <span className='block truncate font-medium'>
              {multiple
                ? values.join(' → ') ||
                  placeholder ||
                  t('Select groups in routing order')
                : selectedOption?.label || placeholder || t('Select a group')}
            </span>
            {!multiple && selectedOption?.desc && (
              <span className='text-muted-foreground block truncate text-[11px] sm:text-xs'>
                {selectedOption.desc}
              </span>
            )}
          </span>
          <span className={multiple ? 'hidden' : 'hidden sm:block'}>
            <GroupRatioBadge
              ratio={selectedOption?.ratio}
              isAuto={isAutoSelected}
              shouldReduceMotion={shouldReduceMotion}
            />
          </span>
        </span>
        <ChevronsUpDown
          aria-hidden='true'
          className='size-4 shrink-0 opacity-50'
        />
      </PopoverTrigger>
      <PopoverContent
        className='data-closed:zoom-out-100 data-open:zoom-in-100 data-[side=bottom]:slide-in-from-top-0 data-[side=left]:slide-in-from-right-0 data-[side=right]:slide-in-from-left-0 data-[side=top]:slide-in-from-bottom-0 w-[var(--anchor-width)] overflow-hidden rounded-xl p-0 shadow-lg data-closed:duration-75 data-open:duration-100'
        onWheel={(event) => event.stopPropagation()}
        onTouchMove={(event) => event.stopPropagation()}
        onPointerDown={(event) => event.stopPropagation()}
      >
        <Command shouldFilter={false}>
          <CommandInput
            placeholder={multiple ? t('Search group name') : t('Search...')}
            value={searchValue}
            onValueChange={setSearchValue}
          />
          <CommandList
            className='max-h-[360px] overscroll-contain'
            aria-label={t('Available groups')}
            aria-multiselectable={multiple || undefined}
          >
            <CommandEmpty>{t('No group found.')}</CommandEmpty>
            <CommandGroup>
              {filteredOptions.map((option) => {
                const isAutoOption = option.value === 'auto'
                const selected = multiple
                  ? values.includes(option.value)
                  : value === option.value

                return (
                  <CommandItem
                    key={option.value}
                    value={option.value}
                    aria-checked={multiple ? selected : undefined}
                    disabled={
                      multiple &&
                      maxCount > 1 &&
                      !selected &&
                      values.length >= maxCount
                    }
                    data-auto-group-effect={isAutoOption ? 'option' : undefined}
                    onSelect={() => handleSelect(option.value)}
                    className={cn(
                      'data-[selected=true]:bg-muted items-start gap-3 rounded-lg px-3 py-3 transition-colors',
                      isAutoOption &&
                        cn(
                          AUTO_GROUP_FRAME_CLASS_NAME,
                          'border-primary/35 data-[selected=true]:border-primary/55'
                        )
                    )}
                  >
                    {isAutoOption && (
                      <AutoGroupFlowBorder
                        shouldReduceMotion={shouldReduceMotion}
                      />
                    )}
                    <Check
                      aria-hidden='true'
                      className={cn(
                        'mt-0.5 size-4',
                        selected ? 'opacity-100' : 'opacity-0'
                      )}
                    />
                    <span className='min-w-0 flex-1'>
                      <span className='flex items-center justify-between gap-2'>
                        <span className='block truncate font-medium'>
                          {option.label}
                        </span>
                        {multiple && (
                          <span className='flex items-center gap-2'>
                            <GroupRatioBadge ratio={option.ratio} />
                            <span
                              className={cn(
                                'size-2 shrink-0 rounded-full bg-muted',
                                option.health?.available === true &&
                                  'bg-emerald-500',
                                option.health?.available === false &&
                                  'bg-red-400'
                              )}
                              aria-label={
                                option.health?.available === true
                                  ? t('Available')
                                  : t('Availability unknown or unavailable')
                              }
                            />
                          </span>
                        )}
                      </span>
                      {option.desc && !multiple && (
                        <span className='text-muted-foreground block truncate text-xs'>
                          {option.desc}
                        </span>
                      )}
                      {multiple && <ApiKeyGroupHealth detail={option.health} />}
                    </span>
                    {!multiple && (
                      <GroupRatioBadge
                        ratio={option.ratio}
                        isAuto={isAutoOption}
                        shouldReduceMotion={shouldReduceMotion}
                      />
                    )}
                  </CommandItem>
                )
              })}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}

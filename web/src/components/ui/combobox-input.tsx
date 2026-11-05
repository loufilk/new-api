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
import { Popover as PopoverPrimitive } from '@base-ui/react/popover'
import { Check, ChevronsUpDown } from 'lucide-react'
import * as React from 'react'
import { useTranslation } from 'react-i18next'

import { Input } from '@/components/ui/input'
import { usePortalContainer } from '@/components/ui/portal-container'
import { cn } from '@/lib/utils'

export type ComboboxInputOption = {
  value: string
  label: string
  icon?: React.ReactNode
  disabled?: boolean
  description?: string
}

interface ComboboxInputProps {
  options: readonly ComboboxInputOption[]
  value?: string
  onValueChange: (value: string) => void
  placeholder?: string
  emptyText?: string
  className?: string
  popupClassName?: string
  id?: string
  allowCustomValue?: boolean
  openOnFocus?: boolean
  disabled?: boolean
  onKeyDown?: React.KeyboardEventHandler<HTMLInputElement>
  'aria-label'?: string
  'aria-labelledby'?: string
  'aria-invalid'?: React.AriaAttributes['aria-invalid']
}

export function ComboboxInput({
  options,
  value = '',
  onValueChange,
  placeholder = 'Select or type...',
  emptyText = 'No option found.',
  className,
  popupClassName,
  id,
  allowCustomValue = false,
  openOnFocus = true,
  disabled = false,
  onKeyDown,
  'aria-label': ariaLabel,
  'aria-labelledby': ariaLabelledBy,
  'aria-invalid': ariaInvalid,
}: ComboboxInputProps) {
  const { t } = useTranslation()
  const listId = React.useId()
  const [open, setOpen] = React.useState(false)
  const [searchValue, setSearchValue] = React.useState('')
  const [searchChanged, setSearchChanged] = React.useState(false)
  const [highlightedIndex, setHighlightedIndex] = React.useState(-1)
  const containerRef = React.useRef<HTMLDivElement>(null)
  const inputRef = React.useRef<HTMLInputElement>(null)
  const listRef = React.useRef<HTMLUListElement>(null)
  const portalContainer = usePortalContainer()
  const [dialogContainer, setDialogContainer] =
    React.useState<HTMLElement | null>(null)
  const pointerFocusRef = React.useRef(false)
  const selectedOption = React.useMemo(
    () => options.find((option) => option.value === value),
    [options, value]
  )
  const displayValue = open ? searchValue : (selectedOption?.label ?? value)

  const filteredOptions = React.useMemo(() => {
    if (!searchChanged || !searchValue.trim()) return options
    const search = searchValue.toLowerCase().trim()
    return options.filter(
      (option) =>
        option.label.toLowerCase().includes(search) ||
        option.value.toLowerCase().includes(search)
    )
  }, [options, searchValue, searchChanged])

  // Reset highlight when filtered options change
  React.useEffect(() => {
    setHighlightedIndex(-1)
  }, [filteredOptions])

  React.useEffect(() => {
    if (open) {
      setDialogContainer(
        inputRef.current?.closest<HTMLElement>('[role="dialog"]') ?? null
      )
    }
  }, [open])

  const handleSelect = (selectedValue: string) => {
    if (
      disabled ||
      options.find((option) => option.value === selectedValue)?.disabled
    )
      return
    onValueChange(selectedValue)
    setOpen(false)
    setSearchValue('')
    inputRef.current?.focus()
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (!open && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
      e.preventDefault()
      setSearchValue(allowCustomValue ? value : '')
      setSearchChanged(false)
      setOpen(true)
      return
    }

    if (!open) return

    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        setHighlightedIndex((prev) => {
          for (let offset = 1; offset <= filteredOptions.length; offset++) {
            const next = (prev + offset) % filteredOptions.length
            if (!filteredOptions[next].disabled) return next
          }
          return -1
        })
        break
      case 'ArrowUp':
        e.preventDefault()
        setHighlightedIndex((prev) => {
          const start = prev < 0 ? 0 : prev
          for (let offset = 1; offset <= filteredOptions.length; offset++) {
            const next =
              (start - offset + filteredOptions.length) % filteredOptions.length
            if (!filteredOptions[next].disabled) return next
          }
          return -1
        })
        break
      case 'Enter':
        if (highlightedIndex >= 0 && filteredOptions[highlightedIndex]) {
          e.preventDefault()
          handleSelect(filteredOptions[highlightedIndex].value)
        } else if (allowCustomValue && searchValue.trim()) {
          e.preventDefault()
          handleSelect(searchValue.trim())
        } else {
          if (!onKeyDown) e.preventDefault()
          // No highlighted option, just close the dropdown and keep current value
          setOpen(false)
          setSearchValue('')
        }
        break
      case 'Escape':
        e.preventDefault()
        e.stopPropagation()
        setOpen(false)
        setSearchValue('')
        break
    }
  }

  // Scroll highlighted item into view
  React.useEffect(() => {
    if (highlightedIndex < 0 || !listRef.current) return
    const item = listRef.current.children[highlightedIndex] as HTMLElement
    item?.scrollIntoView({ block: 'nearest' })
  }, [highlightedIndex])

  const showDropdown =
    open &&
    !disabled &&
    (filteredOptions.length > 0 || (allowCustomValue && searchValue.trim()))

  return (
    <PopoverPrimitive.Root
      open={!!showDropdown}
      onOpenChange={(nextOpen, details) => {
        // The input owns opening; pressing it is not an outside dismissal.
        if (
          !nextOpen &&
          containerRef.current?.contains(details.event.target as Node)
        ) {
          details.cancel()
          return
        }
        setOpen(nextOpen)
        if (!nextOpen) setSearchValue('')
      }}
    >
      <div ref={containerRef} className='relative'>
        <Input
          ref={inputRef}
          id={id}
          type='text'
          role='combobox'
          disabled={disabled}
          aria-label={ariaLabel}
          aria-labelledby={ariaLabelledBy}
          aria-invalid={ariaInvalid}
          aria-expanded={!!showDropdown}
          aria-controls={
            showDropdown && filteredOptions.length > 0 ? listId : undefined
          }
          aria-activedescendant={
            showDropdown && highlightedIndex >= 0
              ? `${listId}-${highlightedIndex}`
              : undefined
          }
          aria-haspopup='listbox'
          aria-autocomplete='list'
          autoComplete='off'
          placeholder={placeholder}
          value={displayValue}
          onChange={(e) => {
            const nextValue = e.target.value
            setSearchValue(nextValue)
            setSearchChanged(true)
            if (allowCustomValue) {
              onValueChange(nextValue)
            }
            if (!open) setOpen(true)
          }}
          onPointerDown={() => {
            pointerFocusRef.current = true
            if (document.activeElement === inputRef.current && !open) {
              setSearchValue(allowCustomValue ? value : '')
              setSearchChanged(false)
              setOpen(true)
            }
          }}
          onFocus={() => {
            setSearchValue(allowCustomValue ? value : '')
            setSearchChanged(false)
            if (openOnFocus || pointerFocusRef.current) {
              setOpen(true)
            }
            pointerFocusRef.current = false
          }}
          onBlur={() => {
            setOpen(false)
            setSearchValue('')
          }}
          onKeyDown={(event) => {
            handleKeyDown(event)
            if (!event.defaultPrevented) onKeyDown?.(event)
          }}
          className={cn('pr-9', className)}
        />
        <ChevronsUpDown className='pointer-events-none absolute top-1/2 right-3 size-4 shrink-0 -translate-y-1/2 opacity-50' />

        <PopoverPrimitive.Portal
          container={portalContainer ?? dialogContainer ?? undefined}
        >
          {/* Position against the actual containing block, including transformed
              drawers, and keep the list within the available viewport. */}
          <PopoverPrimitive.Positioner
            anchor={inputRef}
            sideOffset={4}
            align='start'
            collisionPadding={8}
            className='isolate z-100'
          >
            <PopoverPrimitive.Popup
              role='presentation'
              initialFocus={false}
              finalFocus={false}
              data-slot='combobox-input-popup'
              data-vaul-no-drag
              onPointerDown={(event) => event.stopPropagation()}
              className={cn(
                'bg-popover text-popover-foreground max-h-(--available-height) w-(--anchor-width) max-w-(--available-width) overflow-hidden rounded-md border shadow-md',
                popupClassName
              )}
            >
              {filteredOptions.length > 0 ? (
                <ul
                  ref={listRef}
                  id={listId}
                  role='listbox'
                  className='max-h-[min(200px,var(--available-height))] overflow-y-auto overscroll-contain p-1'
                >
                  {filteredOptions.map((option, index) => (
                    <li
                      key={option.value}
                      id={`${listId}-${index}`}
                      role='option'
                      aria-selected={value === option.value}
                      aria-disabled={option.disabled || undefined}
                      data-highlighted={index === highlightedIndex}
                      className={cn(
                        'relative flex min-h-9 cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm select-none aria-disabled:pointer-events-none aria-disabled:opacity-50',
                        index === highlightedIndex &&
                          'bg-accent text-accent-foreground',
                        value === option.value && 'font-medium'
                      )}
                      onMouseEnter={() => {
                        if (!option.disabled) setHighlightedIndex(index)
                      }}
                      onPointerDown={(e) => e.preventDefault()}
                      onMouseDown={(e) => {
                        e.preventDefault() // Prevent blur
                      }}
                      onClick={() => handleSelect(option.value)}
                    >
                      <Check
                        className={cn(
                          'size-4 shrink-0',
                          value === option.value ? 'opacity-100' : 'opacity-0'
                        )}
                      />
                      {option.icon && <span aria-hidden>{option.icon}</span>}
                      <span className='min-w-0 break-all'>{option.label}</span>
                    </li>
                  ))}
                </ul>
              ) : (
                <div className='px-2 py-6 text-center text-sm'>
                  {t(emptyText)}
                  {allowCustomValue && searchValue.trim() && (
                    <div className='text-muted-foreground mt-1 text-xs'>
                      {t('Press Enter to use "{{value}}"', {
                        value: searchValue.trim(),
                      })}
                    </div>
                  )}
                </div>
              )}
            </PopoverPrimitive.Popup>
          </PopoverPrimitive.Positioner>
        </PopoverPrimitive.Portal>
      </div>
    </PopoverPrimitive.Root>
  )
}

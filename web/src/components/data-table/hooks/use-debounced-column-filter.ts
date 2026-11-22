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
import * as React from 'react'

import type { ColumnFiltersState, OnChangeFn } from '@/lib/console-table'

type UseDebouncedColumnFilterOptions = {
  columnFilters: ColumnFiltersState
  columnId: string
  onColumnFiltersChange: OnChangeFn<ColumnFiltersState>
  delay?: number
}

export function useDebouncedColumnFilter({
  columnFilters,
  columnId,
  onColumnFiltersChange,
  delay = 500,
}: UseDebouncedColumnFilterOptions) {
  const value =
    (columnFilters.find((filter) => filter.id === columnId)?.value as
      | string
      | undefined) ?? ''
  const [draft, setDraft] = React.useState({
    columnId,
    base: value,
    text: value,
    composing: false,
  })
  // An external change (including Back or Forward) wins over the old edit.
  // Clearing the draft also prevents it resurfacing when the old URL returns.
  if (draft.columnId !== columnId || draft.base !== value) {
    setDraft({ columnId, base: value, text: value, composing: false })
  }

  const publish = React.useEffectEvent((edit: typeof draft) => {
    if (edit !== draft || edit.columnId !== columnId || edit.base !== value) {
      return
    }
    onColumnFiltersChange((previous) => {
      const current =
        previous.find((filter) => filter.id === columnId)?.value ?? ''
      if (current !== edit.base) return previous
      const filters = previous.filter((filter) => filter.id !== columnId)
      return edit.text
        ? [...filters, { id: columnId, value: edit.text }]
        : filters
    })
  })

  React.useEffect(() => {
    if (
      draft.composing ||
      draft.text === value ||
      draft.base !== value ||
      draft.columnId !== columnId
    ) {
      return
    }
    const timer = setTimeout(() => publish(draft), delay)
    return () => clearTimeout(timer)
  }, [draft, value, columnId, delay])

  const updateInputValue = React.useCallback((text: string) => {
    setDraft((previous) => ({ ...previous, text }))
  }, [])

  const handleChange = React.useCallback(
    (event: React.ChangeEvent<HTMLInputElement>) => {
      updateInputValue(event.target.value)
    },
    [updateInputValue]
  )

  const handleCompositionStart = React.useCallback(() => {
    setDraft((previous) => ({ ...previous, composing: true }))
  }, [])

  const handleCompositionEnd = React.useCallback(
    (event: React.CompositionEvent<HTMLInputElement>) => {
      const text = event.currentTarget.value
      setDraft((previous) =>
        previous.composing ? { ...previous, text, composing: false } : previous
      )
    },
    []
  )

  const resetInput = React.useCallback(() => {
    setDraft((previous) => ({ ...previous, text: '', composing: false }))
  }, [])

  return {
    value,
    inputValue: draft.text,
    setInputValue: updateInputValue,
    onChange: handleChange,
    onCompositionStart: handleCompositionStart,
    onCompositionEnd: handleCompositionEnd,
    resetInput,
  }
}

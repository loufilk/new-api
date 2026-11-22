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
import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { useDebouncedColumnFilter } from '../use-debounced-column-filter'

function Field(props: { value: string; publish: (next: string) => void }) {
  const filters = [{ id: 'model', value: props.value }]
  const filter = useDebouncedColumnFilter({
    columnId: 'model',
    columnFilters: filters,
    onColumnFiltersChange(updater) {
      const next = typeof updater === 'function' ? updater(filters) : updater
      props.publish(
        String(next.find((item) => item.id === 'model')?.value ?? '')
      )
    },
    delay: 40,
  })
  return (
    <>
      <input
        aria-label='Model'
        value={filter.inputValue}
        onChange={filter.onChange}
        onCompositionStart={filter.onCompositionStart}
        onCompositionEnd={filter.onCompositionEnd}
      />
      <button type='button' onClick={filter.resetInput}>
        Reset
      </button>
    </>
  )
}

beforeEach(() => {
  vi.useFakeTimers()
})
afterEach(() => {
  vi.useRealTimers()
})

it('Back and Forward discard a pending column edit without rewriting the URL', () => {
  const publish = vi.fn()
  const view = render(<Field value='A' publish={publish} />)
  const input = screen.getByRole('textbox', { name: 'Model' })
  fireEvent.change(input, { target: { value: 'unapplied' } })
  view.rerender(<Field value='B' publish={publish} />)
  expect(input).toHaveValue('B')
  view.rerender(<Field value='A' publish={publish} />)
  act(() => {
    vi.advanceTimersByTime(40)
  })
  expect(input).toHaveValue('A')
  expect(publish).not.toHaveBeenCalled()
})

it('publishes only the completed IME string and does not restore an edit after reset', () => {
  const publish = vi.fn()
  render(<Field value='original' publish={publish} />)
  const input = screen.getByRole('textbox', { name: 'Model' })
  fireEvent.change(input, { target: { value: 'preedit' } })
  fireEvent.compositionStart(input)
  fireEvent.change(input, { target: { value: '中文' } })
  act(() => {
    vi.advanceTimersByTime(40)
  })
  expect(publish).not.toHaveBeenCalled()
  fireEvent.compositionEnd(input)
  act(() => {
    vi.advanceTimersByTime(40)
  })
  expect(publish.mock.calls).toEqual([['中文']])
  fireEvent.change(input, { target: { value: 'cancelled' } })
  fireEvent.click(screen.getByRole('button', { name: 'Reset' }))
  act(() => {
    vi.advanceTimersByTime(40)
  })
  expect(input).toHaveValue('')
  expect(publish.mock.calls).toEqual([['中文'], ['']])
})

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
import { act, renderHook } from '@testing-library/react'
import { describe, expect, test, vi } from 'vitest'

import { useTableUrlState } from '../use-table-url-state'

function setup(
  columnFiltersCfg = [{ columnId: 'status', searchKey: 'status' }]
) {
  const navigate = vi.fn()
  const search = {}
  const { result } = renderHook(() =>
    useTableUrlState({
      search,
      navigate,
      columnFilters: columnFiltersCfg,
    })
  )
  return { result, navigate }
}

describe('table url state array boundaries', () => {
  test('ignores missing array filter values instead of throwing', () => {
    const { result } = setup()

    expect(result.current.columnFilters).toEqual([])
  })

  test('serializes selected array values into the URL patch', () => {
    const { result, navigate } = setup()

    act(() => {
      result.current.onColumnFiltersChange([
        { id: 'status', value: ['active', 'disabled'] },
      ])
    })

    expect(result.current.columnFilters).toEqual([
      { id: 'status', value: ['active', 'disabled'] },
    ])
    expect(navigate).toHaveBeenCalledTimes(1)
    const patch = (
      navigate.mock.calls[0][0].search as (
        prev: Record<string, unknown>
      ) => Record<string, unknown>
    )({})
    expect(patch.status).toEqual(['active', 'disabled'])
  })

  test('clears the URL key when the array filter becomes empty', () => {
    const { result, navigate } = setup()

    act(() => {
      result.current.onColumnFiltersChange([{ id: 'status', value: [] }])
    })

    const patch = (
      navigate.mock.calls[0][0].search as (
        prev: Record<string, unknown>
      ) => Record<string, unknown>
    )({})
    expect(patch.status).toBeUndefined()
  })
})
